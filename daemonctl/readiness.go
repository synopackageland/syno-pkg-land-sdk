package main

// Bridge readiness is deliberately availability evidence, not Docker identity
// attestation. The lifecycle record does not identify the actual Docker process.
import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/composeingress"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type healthProbe struct {
	Path   string `json:"path"`
	Status int    `json:"status"`
}
type bridgeReadiness struct {
	Version    string      `json:"version"`
	PlanSHA256 string      `json:"plan_sha256"`
	Health     healthProbe `json:"health"`
}
type acquireRecord struct {
	Schema     int    `json:"schema"`
	Package    string `json:"package"`
	Version    string `json:"version"`
	PlanSHA256 string `json:"plan_sha256"`
	Generation string `json:"generation"`
	Active     bool   `json:"active"`
}

func (c *controller) bridgePolicy() (*bridgeReadiness, error) {
	if c.Network == nil || c.Network.Mode != "bridge" {
		return nil, nil
	}
	if c.Network.Ingress != nil {
		if _, err := composeingress.Allocate(c.Package, c.Network.Ingress.Candidate); err != nil {
			return nil, fmt.Errorf("COMPOSE_READINESS_INVALID: %w", err)
		}
	}
	p := c.Network.Readiness
	if p == nil || p.Version == "" || len(p.PlanSHA256) != 64 || len(c.Network.Ports) != 1 {
		return nil, errors.New("COMPOSE_READINESS_INVALID: bridge requires health, one fixed TCP endpoint and a versioned lifecycle record")
	}
	if _, err := hex.DecodeString(p.PlanSHA256); err != nil {
		return nil, errors.New("COMPOSE_READINESS_INVALID: invalid plan digest")
	}
	path, err := url.ParseRequestURI(p.Health.Path)
	if err != nil || !strings.HasPrefix(p.Health.Path, "/") || strings.HasPrefix(p.Health.Path, "//") || strings.ContainsAny(p.Health.Path, "\r\n#\\") || path.Host != "" || path.IsAbs() || p.Health.Status < 200 || p.Health.Status > 299 || c.Network.Ports[0] < 1 || c.Network.Ports[0] > 65535 {
		return nil, errors.New("COMPOSE_READINESS_INVALID: health must use a relative origin path and a 2xx expected status")
	}
	return p, nil
}
func (c *controller) installedVersion() (string, error) {
	path := c.installedInfo
	if path == "" {
		path = filepath.Join("/var/packages", c.Package, "INFO")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 512*1024 {
		return "", errors.New("COMPOSE_ACQUIRE_GENERATION_INVALID: installed INFO unavailable")
	}
	version := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "version=") {
			if version != "" {
				return "", errors.New("COMPOSE_ACQUIRE_GENERATION_INVALID: ambiguous installed version")
			}
			version = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "version=")), "\"")
		}
	}
	if version == "" {
		return "", errors.New("COMPOSE_ACQUIRE_GENERATION_INVALID: installed version unavailable")
	}
	return version, nil
}

// Called only from the generated package-account lifecycle, after DSM's startup
// worker has returned (acquire), or before stop/upgrade/uninstall (release).
// This records what our lifecycle observed; it never claims Docker attestation.
func (c *controller) recordAcquire(active bool) error {
	p, err := c.bridgePolicy()
	if err != nil || p == nil {
		return err
	}
	version, err := c.installedVersion()
	if err != nil {
		return err
	}
	if active && version != p.Version {
		return errors.New("COMPOSE_ACQUIRE_GENERATION_INVALID: installed version differs from lifecycle configuration")
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	r := acquireRecord{1, c.Package, p.Version, p.PlanSHA256, hex.EncodeToString(nonce), active}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(c.dir, ".compose-acquire-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(raw, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(c.dir, "compose-acquire.json")); err != nil {
		return err
	}
	d, err := os.Open(c.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (c *controller) checkReadiness() error {
	return c.checkReadinessUntil(time.Now().Add(c.probeBudget()))
}

func (c *controller) probeBudget() time.Duration {
	if c.Timeouts.Status > 0 {
		return min(time.Duration(c.Timeouts.Status)*time.Second, c.operationBudget())
	}
	return 5 * time.Second
}

func (c *controller) checkReadinessUntil(until time.Time) error {
	ctx, cancel := context.WithDeadline(context.Background(), until)
	defer cancel()
	if c.Network == nil || c.Network.Mode != "bridge" {
		return checkNetwork("/proc/net", os.Geteuid(), c.Network)
	}
	p, err := c.bridgePolicy()
	if err != nil {
		return err
	}
	version, err := c.installedVersion()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(c.dir, "compose-acquire.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("COMPOSE_ACQUIRE_GENERATION_INVALID: %w", err)
	}
	st, statErr := file.Stat()
	if statErr != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || !owned(st) || st.Sys().(*syscall.Stat_t).Nlink != 1 {
		file.Close()
		return errors.New("COMPOSE_ACQUIRE_GENERATION_INVALID: foreign lifecycle state")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 16*1024))
	file.Close()
	var r acquireRecord
	if err != nil || json.Unmarshal(raw, &r) != nil || r.Schema != 1 || r.Package != c.Package || !r.Active || r.Version != p.Version || version != p.Version || r.PlanSHA256 != p.PlanSHA256 || len(r.Generation) != 32 {
		return errors.New("COMPOSE_ACQUIRE_GENERATION_INVALID: missing, released or stale lifecycle generation")
	}
	if _, err := hex.DecodeString(r.Generation); err != nil {
		return errors.New("COMPOSE_ACQUIRE_GENERATION_INVALID: invalid lifecycle generation")
	}
	if c.acquireGeneration != "" && c.acquireGeneration != r.Generation {
		return errors.New("COMPOSE_ACQUIRE_GENERATION_INVALID: lifecycle generation changed")
	}
	c.acquireGeneration = r.Generation
	host := "127.0.0.1"
	if c.Network.Ingress != nil {
		a, e := composeingress.Allocate(c.Package, c.Network.Ingress.Candidate)
		if e != nil {
			return fmt.Errorf("COMPOSE_READINESS_INVALID: %w", e)
		}
		host = a.Address
	}
	address := net.JoinHostPort(host, strconv.Itoa(c.Network.Ports[0]))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", address)
	if err != nil {
		return fmt.Errorf("COMPOSE_TCP_NOT_READY: %w", err)
	}
	conn.Close()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: (&net.Dialer{}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+address+p.Health.Path, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("COMPOSE_HEALTH_NOT_READY: %w", err)
	}
	response.Body.Close()
	if response.StatusCode != p.Health.Status {
		return fmt.Errorf("COMPOSE_HEALTH_NOT_READY: got HTTP %d, expected %d", response.StatusCode, p.Health.Status)
	}
	return nil
}
