package compose

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// WaitForWorkerProfiles waits for Container Manager's asynchronous profile
// registration before a START worker attempts to enable the project's containers.
// It observes file metadata only, never reads private profile contents, opens a
// Docker socket, or repairs DSM-owned files. Presence is not identity attestation.
// Missing profiles remain pending until cancellation; elapsed time is no failure.
//
// Deprecated: Container Manager may remove profiles on STOP and recreate them
// during START. Never use this helper as a package lifecycle precondition.
func WaitForWorkerProfiles(ctx context.Context, project string, waiting func()) error {
	if os.Getuid() == 0 || os.Getuid() != os.Geteuid() {
		return problem("COMPOSE_ROOT_FORBIDDEN", "worker preparation must run as the package account")
	}
	f, err := os.OpenFile(filepath.Join(project, "compose.lock.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || !st.Mode().IsRegular() || st.Size() > 4<<20 || owner.Nlink != 1 {
		return problem("COMPOSE_LOCK_INVALID", "invalid worker profile inventory")
	}
	paths, err := workerProfilePaths(io.LimitReader(f, 4<<20))
	if err != nil {
		return err
	}
	return waitWorkerProfiles(ctx, os.DirFS("/"), paths, waiting)
}

func workerProfilePaths(in io.Reader) ([]string, error) {
	var lock struct {
		Schema      int    `json:"schema"`
		Package     string `json:"package"`
		ProjectName string `json:"project_name"`
		Services    []struct {
			Service string `json:"service"`
		} `json:"services"`
	}
	d := json.NewDecoder(in)
	invalid := func() ([]string, error) {
		return nil, problem("COMPOSE_LOCK_INVALID", "invalid worker profile inventory")
	}
	if d.Decode(&lock) != nil || d.Decode(new(any)) != io.EOF || lock.Schema != PlannerVersion || !packageName.MatchString(lock.Package) || len(lock.Services) == 0 {
		return invalid()
	}
	prefix, matches := strings.CutSuffix(lock.ProjectName, strings.ToLower(lock.Package))
	if !matches || len(prefix) > 32 || (prefix != "" && !regexp.MustCompile(`^[a-z][a-z0-9-]*-$`).MatchString(prefix)) {
		return invalid()
	}
	var paths []string
	seen := map[string]bool{}
	for _, s := range lock.Services {
		name := lock.ProjectName + "-" + s.Service + "-1.config"
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(s.Service) || len(name) > 255 || seen[s.Service] {
			return invalid()
		}
		seen[s.Service] = true
		paths = append(paths, "var/packages/ContainerManager/etc/"+name)
	}
	return paths, nil
}

func waitWorkerProfiles(ctx context.Context, root fs.FS, paths []string, waiting func()) error {
	announced := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ready := true
		for _, path := range paths {
			// ReadDir is unnecessary; Stat does not open the root-owned 0600 file.
			st, err := fs.Stat(root, path)
			if errors.Is(err, fs.ErrNotExist) {
				ready = false
				continue
			}
			if err != nil || !st.Mode().IsRegular() {
				return problem("COMPOSE_WORKER_PROFILE_UNAVAILABLE", "cannot inspect this App's Container Manager profile; repair this App in Package Center, then start it again")
			}
			if st.Size() == 0 {
				ready = false
			}
		}
		if ready {
			return nil
		}
		if !announced && waiting != nil {
			waiting()
			announced = true
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
