//go:build contractserver

// Contract server for the Go client tests in delegate/. Only DSM, peer
// credentials and the bulk writer are substituted; the v1 decoder,
// grant/store, async journal and publication fence are production.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type contractDSM struct{ root string }

func (d contractDSM) Verify(_ context.Context, cookie, token string) (string, error) {
	if token != "contract-token" || (cookie != "alice" && cookie != "bob" && cookie != "admin") {
		return "", errors.New("invalid session")
	}
	return cookie, nil
}
func (d contractDSM) Account(_ context.Context, name string) (Subject, error) {
	uid := 1205
	if name == "bob" {
		uid = 1206
	}
	if name == "admin" {
		uid = 1207
	}
	return Subject{Username: name, UID: uid, GID: 100, Groups: []int{100}, Home: d.root, Expired: "normal", Admin: name == "admin"}, nil
}
func (d contractDSM) Allow(context.Context, Subject, string, string) error       { return nil }
func (d contractDSM) ScopeRoot(context.Context, Subject, string) (string, error) { return d.root, nil }
func (d contractDSM) API(context.Context, string, string, map[string]any) (json.RawMessage, error) {
	return json.RawMessage(`{"allowed":true}`), nil
}

type contractWriter struct{ control string }

func (w contractWriter) Write(context.Context, Subject, string, string, string, string) (MoveReceipt, error) {
	return MoveReceipt{}, errors.New("not a legacy writer test")
}
func (w contractWriter) Move(ctx context.Context, _ Subject, op Operation, d Delegation, guard func(func() (MoveReceipt, error)) (MoveReceipt, error)) (MoveReceipt, error) {
	f, err := os.OpenFile(filepath.Join(w.control, "attempt-"+op.Job), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return MoveReceipt{}, err
	}
	_, err = f.WriteString("attempt\n")
	f.Close()
	if err != nil {
		return MoveReceipt{}, err
	}
	if strings.HasPrefix(op.Job, "stuck-") {
		// Models a write that does not observe cancellation until released.
		for {
			if _, err = os.Stat(filepath.Join(w.control, "release-"+op.Job)); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if strings.HasPrefix(op.Job, "hold-") {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err = os.Stat(filepath.Join(w.control, "release-"+op.Job)); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return MoveReceipt{}, ctx.Err()
			case <-ticker.C:
			}
		}
	}
	return guard(func() (MoveReceipt, error) {
		if strings.HasPrefix(op.Job, "uncertain-") {
			return MoveReceipt{}, errors.New("publication reply lost")
		}
		return MoveReceipt{Path: d.Path + "/" + op.Name}, nil
	})
}

func TestContractServer(t *testing.T) {
	dir := os.Getenv("CONTRACT_DIR")
	if dir == "" {
		t.Fatal("CONTRACT_DIR is required")
	}
	e := &Executor{Policy: Policy{Version: 1, Package: "contract", App: "app", Scope: "home", Operations: []string{"delegate.create", "delegate.list", "delegate.revoke", "files.write", "files.moveFromStaging", "files.operationStatus", "files.cancelOperation", "delegate.disable", "delegate.enable", "delegate.audit"}, Destinations: []string{"save"}, Lifetime: "until-revoked"}, DSM: contractDSM{filepath.Join(dir, "home")}, Writer: contractWriter{dir}, Store: &Store{Dir: filepath.Join(dir, "protected")}, Now: time.Now}
	if err := loadContractVector(dir, e); err != nil {
		t.Fatal(err)
	}
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	socket := filepath.Join(dir, "executor.sock")
	os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for {
		c, err := listener.Accept()
		if err != nil {
			t.Fatal(err)
		}
		go e.Connection(context.Background(), c, os.Getuid(), func(net.Conn) (int, error) { return os.Getuid(), nil })
	}
}

// loadContractVector applies a conformance vector's policy.json and
// state.json when present; the Go client tests use the built-in policy.
func loadContractVector(dir string, e *Executor) error {
	if b, err := os.ReadFile(filepath.Join(dir, "policy.json")); err == nil {
		var policy Policy
		if err = json.Unmarshal(b, &policy); err != nil {
			return err
		}
		e.Policy = policy
		if policy.Portal != nil {
			e.Portal = &contractPortal{entry: PortalUnregistered}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var state struct {
		Disabled bool            `json:"disabled"`
		Grants   []contractGrant `json:"grants"`
	}
	if err = json.Unmarshal(b, &state); err != nil {
		return err
	}
	if err = os.MkdirAll(e.Store.Dir, 0700); err != nil {
		return err
	}
	for _, g := range state.Grants {
		if err = g.put(e); err != nil {
			return err
		}
	}
	return e.Store.SetDisabled(state.Disabled)
}

// contractPortal stands in for DSM's app.d and nginx: the entry is what the
// last change made it.
type contractPortal struct{ entry string }

func (p *contractPortal) Register(context.Context) error   { p.entry = PortalRegistered; return nil }
func (p *contractPortal) Unregister(context.Context) error { p.entry = PortalUnregistered; return nil }
func (p *contractPortal) Entry() string                    { return p.entry }

// contractGrant is an until-revoked home destination grant, as
// delegate.create would store it, with a fixed id.
type contractGrant struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Key      string `json:"key"`
	Path     string `json:"path"`
}

func (g contractGrant) put(e *Executor) error {
	s, err := e.DSM.Account(context.Background(), g.Username)
	if err != nil {
		return err
	}
	real := filepath.Join(s.Home, strings.TrimPrefix(g.Path, "/home"))
	if err = os.MkdirAll(real, 0700); err != nil {
		return err
	}
	d := Delegation{ID: g.ID, Package: e.Policy.Package, Subject: s, Ops: []string{"files.moveFromStaging"}, Scope: "home", Path: g.Path, Root: real, Lifetime: "until-revoked", DestinationKey: g.Key}
	if d.Directory, err = bindDirectory(real); err != nil {
		return err
	}
	return e.Store.Put(d)
}
