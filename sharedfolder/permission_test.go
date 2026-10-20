package sharedfolder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func TestConfiguredPathsAndPermissionFailuresShareOneError(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o555)
	cfg.Account = ""
	target := Target{Key: "library", Name: "Media", Path: filepath.Join(cfg.Root, "volume1", "Media"), Required: Write}
	preflight := cfg.Check(target)
	var before *PermissionError
	if !errors.As(preflight, &before) || before.Account == "" || before.Need != Write || before.Name != "Media" {
		t.Fatalf("preflight: %v", preflight)
	}
	for _, cause := range []error{syscall.EACCES, syscall.EPERM} {
		failure := &os.PathError{Op: "open", Path: filepath.Join(target.Path, "nested.txt"), Err: cause}
		err := cfg.WrapError(target, fmt.Errorf("save: %w", failure))
		var perm *PermissionError
		if !errors.As(err, &perm) || !errors.Is(err, cause) || !errors.Is(err, ErrPermissionPending) || perm.Error() != before.Error() {
			t.Fatalf("runtime: %v", err)
		}
		result := api.BatchFailure(err)
		raw, _ := json.Marshal(result)
		for _, text := range []string{`"code":"shared_folder_permission"`, `"account":`, `"share":"Media"`, `"required":"write"`} {
			if !strings.Contains(string(raw), text) {
				t.Fatalf("missing %s: %s", text, raw)
			}
		}
	}
	other := &os.PathError{Op: "open", Path: target.Path, Err: syscall.ENOSPC}
	if cfg.WrapError(target, nil) != nil || cfg.WrapError(target, other) != other {
		t.Fatal("unrelated errors must pass through")
	}
	target.Required = Read
	if err := cfg.Check(target); err != nil {
		t.Fatalf("read-only target: %v", err)
	}
	target.Path = filepath.Join(target.Path, "missing")
	if err := cfg.Check(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing is not denied: %v", err)
	}
}

func TestStatusHandlerChecksOnlyRegisteredPaths(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o555)
	cfg.Account = ""
	calls := 0
	routes := cfg.Routes(func(context.Context) ([]Target, error) {
		calls++
		return []Target{{Key: "library", Name: "Media", Path: filepath.Join(cfg.Root, "volume1", "Media"), Required: Write}}, nil
	})
	data, err := routes[0].Invoke(context.Background(), api.Caller{}, nil, json.RawMessage(`{}`))
	status, ok := data.(Status)
	if err != nil || !ok || !status.Pending() || status.Folders[0].Key != "library" || calls != 1 {
		t.Fatalf("%+v %v", data, err)
	}
	if _, err := routes[0].Invoke(context.Background(), api.Caller{}, nil, json.RawMessage(`{"path":"/other"}`)); err == nil || calls != 1 {
		t.Fatal("request must not choose a path")
	}
}

func TestPermissionChecksRefuseWrongAccount(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o755)
	cfg.Account = "a-different-package-account"
	if _, err := cfg.Status(); !errors.Is(err, ErrIdentity) {
		t.Fatalf("%v", err)
	}
	if _, err := cfg.Require("media"); !errors.Is(err, ErrIdentity) {
		t.Fatalf("%v", err)
	}
	if err := cfg.Check(Target{Path: t.TempDir(), Required: Read}); !errors.Is(err, ErrIdentity) {
		t.Fatalf("%v", err)
	}
}

func TestPermissionChecksRefuseRoot(t *testing.T) {
	if os.Getuid() != 0 && os.Geteuid() != 0 {
		t.Skip("root-only refusal probe")
	}
	cfg := Config{}
	if _, err := cfg.Status(); !errors.Is(err, ErrIdentity) {
		t.Fatalf("status: %v", err)
	}
	if _, err := cfg.Require("media"); !errors.Is(err, ErrIdentity) {
		t.Fatalf("require: %v", err)
	}
	if err := cfg.Check(Target{Path: t.TempDir(), Required: Write}); !errors.Is(err, ErrIdentity) {
		t.Fatalf("check: %v", err)
	}
	if err := cfg.WrapError(Target{}, syscall.EACCES); !errors.Is(err, ErrIdentity) {
		t.Fatalf("wrap: %v", err)
	}
}

func TestRealChildDenialAfterSuccessfulDirectoryCheck(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o755)
	target := Target{Key: "media", Name: "Media", Path: filepath.Join(cfg.Root, "volume1", "Media"), Required: Read}
	child := filepath.Join(target.Path, "restricted.txt")
	if err := os.WriteFile(child, []byte("private"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Check(target); err != nil {
		t.Fatalf("directory should be accessible: %v", err)
	}
	_, ioErr := os.ReadFile(child)
	if !errors.Is(ioErr, syscall.EACCES) {
		t.Fatalf("expected real EACCES: %v", ioErr)
	}
	err := cfg.WrapError(target, ioErr)
	var perm *PermissionError
	if !errors.As(err, &perm) || !errors.Is(err, syscall.EACCES) || perm.Need != Read {
		t.Fatalf("%v", err)
	}
	if strings.Contains(api.BatchFailure(err).Error.Message, child) {
		t.Fatal("public error must not expose child path")
	}
}
