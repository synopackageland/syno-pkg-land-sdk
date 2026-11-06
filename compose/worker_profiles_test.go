package compose

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestWaitForWorkerProfilesUsesBoundedOwnedLock(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("package account required")
	}
	project := t.TempDir()
	lock := filepath.Join(project, "compose.lock.json")
	if err := os.WriteFile(lock, []byte(`{"schema":1,"package":"ExampleApp","project_name":"exampleapp","services":[{"service":"web"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitForWorkerProfiles(ctx, project, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("valid lock did not preserve cancellation: %v", err)
	}
	if err := os.Rename(lock, lock+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(lock+".real", lock); err != nil {
		t.Fatal(err)
	}
	if err := WaitForWorkerProfiles(ctx, project, nil); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("symlinked lock accepted: %v", err)
	}
}

func TestWorkerProfilePathsUseOnlyOwnedProjectServices(t *testing.T) {
	valid := `{"schema":1,"package":"ExampleApp","project_name":"spl-exampleapp","services":[{"service":"web"},{"service":"broker"}]}`
	paths, err := workerProfilePaths(strings.NewReader(valid))
	if err != nil || strings.Join(paths, ",") != "var/packages/ContainerManager/etc/spl-exampleapp-web-1.config,var/packages/ContainerManager/etc/spl-exampleapp-broker-1.config" {
		t.Fatal(paths, err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"web"`, `"../foreign"`, 1),
		strings.Replace(valid, `"broker"`, `"web"`, 1),
		strings.Replace(valid, "spl-exampleapp", "foreign-project", 1),
		strings.Replace(valid, "spl-exampleapp", "../exampleapp", 1),
		strings.Replace(valid, `"schema":1`, `"schema":2`, 1),
		`{"schema":1,"package":"ExampleApp","project_name":"exampleapp","services":[]}`,
		valid + `{}`,
	} {
		if paths, err := workerProfilePaths(strings.NewReader(raw)); err == nil {
			t.Fatalf("unsafe inventory accepted: %v", paths)
		}
	}
}

type delayedProfileFS struct {
	fstest.MapFS
	calls int
	err   error
}

func (f *delayedProfileFS) Stat(name string) (fs.FileInfo, error) {
	f.calls++
	if name == "broker" && f.calls < 6 {
		return nil, f.err
	}
	return fs.Stat(f.MapFS, name)
}

func TestWorkerProfilesWaitForEveryServiceAndAnnounceOnce(t *testing.T) {
	f := &delayedProfileFS{MapFS: fstest.MapFS{"web": {Data: []byte("ready")}, "broker": {Data: []byte("ready")}}, err: fs.ErrNotExist}
	announcements := 0
	if err := waitWorkerProfiles(context.Background(), f, []string{"web", "broker"}, func() { announcements++ }); err != nil {
		t.Fatal(err)
	}
	if f.calls != 6 || announcements != 1 {
		t.Fatalf("returned before registration completed or repeated message: calls=%d messages=%d", f.calls, announcements)
	}
}

func TestWorkerProfilesStayPendingUntilExplicitCancellation(t *testing.T) {
	for _, f := range []fstest.MapFS{{}, {"web": {Data: nil}}} {
		ctx, cancel := context.WithCancel(context.Background())
		err := waitWorkerProfiles(ctx, f, []string{"web"}, cancel)
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("missing/empty profile was admitted or converted to timeout: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitWorkerProfiles(ctx, fstest.MapFS{"web": {Data: []byte("ready")}}, []string{"web"}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("ignored explicit cancellation: %v", err)
	}
}

func TestWorkerProfilesRefuseUnreadableAndNonregularEvidence(t *testing.T) {
	for _, f := range []fs.FS{
		&delayedProfileFS{err: fs.ErrPermission},
		fstest.MapFS{"broker": {Mode: fs.ModeDir}},
	} {
		err := waitWorkerProfiles(context.Background(), f, []string{"broker"}, nil)
		if err == nil || !strings.Contains(err.Error(), "COMPOSE_WORKER_PROFILE_UNAVAILABLE") || !strings.Contains(err.Error(), "Manual Install") {
			t.Fatalf("unknown profile evidence admitted: %v", err)
		}
	}
}

func TestInstalledStartMissingProfilesFailsEvenWhileContainerEndpointRuns(t *testing.T) {
	// A reachable upstream cannot substitute for DSM worker metadata.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, root := range []fstest.MapFS{{}, {"web": {Data: nil}}, {"web": {Data: []byte("ready")}}} {
		err := checkWorkerProfiles(root, []string{"web", "broker"})
		if !errors.Is(err, errWorkerProfilePending) || !strings.Contains(err.Error(), "Manual Install") || !strings.Contains(err.Error(), "same-version SPK") {
			t.Fatalf("registered project with missing profiles must fail with recovery: %v", err)
		}
	}
}

func TestInstalledProfileCheckUsesOwnedLockAndNeverWaits(t *testing.T) {
	project := t.TempDir()
	lock := filepath.Join(project, "compose.lock.json")
	if err := os.WriteFile(lock, []byte(`{"schema":1,"package":"ExampleApp","project_name":"exampleapp","services":[{"service":"web"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckWorkerProfiles(project); !errors.Is(err, errWorkerProfilePending) {
		t.Fatalf("missing installed profile must return directly: %v", err)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("outside", lock); err != nil {
		t.Fatal(err)
	}
	if err := CheckWorkerProfiles(project); err == nil || errors.Is(err, errWorkerProfilePending) {
		t.Fatalf("symlinked lock accepted: %v", err)
	}
}

type metadataOnlyProfiles struct {
	fstest.MapFS
}

func (f metadataOnlyProfiles) Open(string) (fs.File, error) {
	panic("DSM profile contents must never be opened")
}

func (f metadataOnlyProfiles) Stat(name string) (fs.FileInfo, error) {
	return fs.Stat(f.MapFS, name)
}

func TestWorkerProfilesUseMetadataOnly(t *testing.T) {
	root := metadataOnlyProfiles{fstest.MapFS{"web": {Data: []byte("private"), Mode: 0600}}}
	if err := checkWorkerProfiles(root, []string{"web"}); err != nil {
		t.Fatal(err)
	}
	if err := waitWorkerProfiles(context.Background(), root, []string{"web"}, nil); err != nil {
		t.Fatal(err)
	}
}
