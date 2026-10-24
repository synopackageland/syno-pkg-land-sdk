package compose

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/distribution/reference"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/composeimages"
)

// ImageCause is shared by native operation diagnostics and read-only checks.
// Wrapper codes (including Compose-up failures) never identify an image cause.
type ImageCause string

const (
	ImageTLSTrust    ImageCause = "tls_trust"
	ImageUnreachable ImageCause = "unreachable"
	ImageDNS         ImageCause = "dns"
	ImageRateLimit   ImageCause = "rate_limit"
	ImageDiskFull    ImageCause = "disk_full"
	ImageNotFound    ImageCause = "not_found"
	ImageDenied      ImageCause = "denied"
	ImageUnknown     ImageCause = "unknown"
)

// ImageDiagnosis contains no response bodies, URLs, credentials or native log
// text. Source distinguishes a registry check from an actual Docker operation.
type ImageDiagnosis struct {
	Schema       int        `json:"schema"`
	Code         string     `json:"code"`
	Cause        ImageCause `json:"cause"`
	MessageKey   string     `json:"message_key"`
	Source       string     `json:"source"`
	Phase        string     `json:"phase"`
	RegistryHost string     `json:"registry_host,omitempty"`
	ObservedAt   string     `json:"observed_at"`
	Retryable    bool       `json:"retryable"`
	Detail       string     `json:"sanitized_detail"`
	HTTPStatus   int        `json:"http_status,omitempty"`
}

func imageDiagnosis(cause ImageCause, source, phase, host, detail string) ImageDiagnosis {
	return ImageDiagnosis{Schema: 1, Code: "COMPOSE_IMAGE_" + strings.ToUpper(string(cause)), Cause: cause,
		MessageKey: "compose.diagnosis." + string(cause), Source: source, Phase: phase, RegistryHost: host,
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Retryable: cause != ImageNotFound && cause != ImageUnknown, Detail: detail}
}

// ClassifyImageFailure requires positive lower-level evidence. It does not
// retain the supplied text. Callers must not pass wrapper codes as a cause.
func ClassifyImageFailure(message string) ImageCause {
	if len(message) > 16384 {
		return ImageUnknown
	}
	text := strings.ToLower(message)
	for _, rule := range []struct {
		cause ImageCause
		terms []string
	}{
		{ImageTLSTrust, []string{"certificate signed by unknown authority", "self-signed certificate", "self signed certificate", "certificate verify failed"}},
		{ImageDNS, []string{"no such host", "temporary failure in name resolution", "dns lookup failed"}},
		{ImageRateLimit, []string{"toomanyrequests", "too many requests", "http status 429"}},
		{ImageDiskFull, []string{"no space left on device", "disk quota exceeded"}},
		{ImageNotFound, []string{"manifest unknown", "manifest not found", "name unknown"}},
		{ImageDenied, []string{"unauthorized", "authentication required", "pull access denied", "requested access to the resource is denied"}},
		{ImageUnreachable, []string{"connection refused", "network is unreachable", "no route to host", "i/o timeout", "context deadline exceeded"}},
	} {
		for _, term := range rule.terms {
			if strings.Contains(text, term) {
				return rule.cause
			}
		}
	}
	return ImageUnknown
}

// DiagnoseImageFailure adapts a known native operation failure without saving
// its potentially sensitive stderr. The runtime probe never calls this with
// a guessed or unavailable worker result.
func DiagnoseImageFailure(message, phase string) ImageDiagnosis {
	return imageDiagnosis(ClassifyImageFailure(message), "container_manager", phase, "", "native_operation_failed")
}

const registryCheckBudget = 5 * time.Second
const registryRequestBudget = 2 * time.Second

// CheckRegistries is informational. It only contacts HTTPS /v2/ on at most
// four distinct registries from immutable image references in compose.lock.json.
// A registry is skipped when this package has already run every locked image
// from it: Start and a same-image upgrade then never need to download.
// It does not read Docker configuration, credentials, sockets or worker logs.
func CheckRegistries(ctx context.Context, project, phase string) []ImageDiagnosis {
	return checkRegistries(ctx, project, phase, nil)
}

func checkRegistries(ctx context.Context, project, phase string, transport http.RoundTripper) []ImageDiagnosis {
	hosts, err := lockedRegistryHosts(project)
	if err != nil {
		return []ImageDiagnosis{imageDiagnosis(ImageUnknown, "registry_check", phase, "", "lock_unavailable_or_invalid")}
	}
	hosts = slices.DeleteFunc(hosts, func(h lockedHost) bool { return h.cached })
	if len(hosts) == 0 {
		return []ImageDiagnosis{}
	}
	ctx, cancel := context.WithTimeout(ctx, registryCheckBudget)
	defer cancel()
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	if tr, ok := transport.(*http.Transport); ok {
		tr = tr.Clone()
		tr.Proxy = nil
		tr.TLSHandshakeTimeout = registryRequestBudget
		tr.ResponseHeaderTimeout = registryRequestBudget
		tr.MaxResponseHeaderBytes = 16384
		tr.DisableKeepAlives = true
		transport = tr
		defer tr.CloseIdleConnections()
	}
	client := &http.Client{Transport: transport, Timeout: registryRequestBudget,
		// Even same-host redirects are unnecessary for the registry API check. Never
		// follow a token realm or a redirect to an unapproved host/scheme.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	results := []ImageDiagnosis{}
	for _, locked := range hosts {
		host := locked.name
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/v2/", nil)
		resp, err := client.Do(req)
		if err != nil {
			cause := classifyRegistryError(err)
			results = append(results, imageDiagnosis(cause, "registry_check", phase, host, "registry_request_failed"))
			continue
		}
		// No response body (which may contain tokens or proxy login pages) is read,
		// saved or printed. 401 is a normal anonymous registry challenge.
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
			continue
		}
		cause := ImageUnknown
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			cause = ImageRateLimit
		case resp.StatusCode == http.StatusForbidden:
			cause = ImageDenied
		case resp.StatusCode >= 500:
			cause = ImageUnreachable
		}
		// A 404 on /v2/ does not prove any particular image is missing.
		d := imageDiagnosis(cause, "registry_check", phase, host, fmt.Sprintf("registry_http_%d", resp.StatusCode))
		d.HTTPStatus = resp.StatusCode
		results = append(results, d)
	}
	return results
}

func classifyRegistryError(err error) ImageCause {
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	if errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostname) {
		return ImageTLSTrust
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return ImageDNS
	}
	var op *net.OpError
	if errors.As(err, &op) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ImageUnreachable
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return ImageUnreachable
	}
	return ImageUnknown
}

func readDiagnosisFile(path string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("invalid diagnosis input")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("diagnosis input too large")
	}
	return data, nil
}

type lockedHost struct {
	name   string
	cached bool
}

func lockedRegistryHosts(project string) ([]lockedHost, error) {
	st, err := os.Lstat(project)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("invalid project")
	}
	raw, err := readDiagnosisFile(filepath.Join(project, "compose.lock.json"), 4<<20)
	if err != nil {
		return nil, err
	}
	var lock struct {
		Schema  int     `json:"schema"`
		Package string  `json:"package"`
		Images  []Image `json:"images"`
	}
	if json.Unmarshal(raw, &lock) != nil || lock.Schema != 1 || !packageName.MatchString(lock.Package) || len(lock.Images) == 0 || len(lock.Images) > 64 {
		return nil, errors.New("invalid image lock")
	}
	hosts := map[string]bool{}
	ran := composeimages.Remembered(filepath.Join(packageStateDirectory(lock.Package, ""), "daemon"), lock.Package)
	for _, image := range lock.Images {
		named, err := reference.ParseNormalizedNamed(image.Reference)
		if err != nil || !image.Pinned {
			return nil, errors.New("unlocked image")
		}
		if _, ok := named.(reference.Canonical); !ok {
			return nil, errors.New("image is not immutable")
		}
		host := reference.Domain(named)
		if host == "docker.io" {
			host = "registry-1.docker.io"
		}
		u, err := url.Parse("https://" + host)
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("invalid registry")
		}
		cached, seen := hosts[host]
		hosts[host] = (cached || !seen) && ran[image.Reference]
		if len(hosts) > 4 {
			return nil, errors.New("too many registries")
		}
	}
	result := make([]lockedHost, 0, len(hosts))
	for _, host := range slices.Sorted(maps.Keys(hosts)) {
		result = append(result, lockedHost{host, hosts[host]})
	}
	return result, nil
}
