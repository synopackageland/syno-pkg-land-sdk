package compose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// registration on a path that can still generate the project's profiles.
// It observes file metadata only, never reads private profile contents, opens a
// Docker socket, or repairs DSM-owned files. Presence is not identity attestation.
// Missing profiles remain pending until cancellation; elapsed time is no failure.
func WaitForWorkerProfiles(ctx context.Context, project string, waiting func()) error {
	paths, err := loadWorkerProfilePaths(project)
	if err != nil {
		return err
	}
	return waitWorkerProfiles(ctx, os.DirFS("/"), paths, waiting)
}

// CheckWorkerProfiles checks an already installed project's worker prerequisites.
// START only enables existing containers; it cannot regenerate missing profiles.
// Fail with explicit recovery instead of waiting for an INSTALL/UPGRADE operation
// that cannot run while the current START is pending. This is metadata evidence,
// not proof of container identity or of a successful worker stop.
func CheckWorkerProfiles(project string) error {
	paths, err := loadWorkerProfilePaths(project)
	if err != nil {
		return err
	}
	return checkWorkerProfiles(os.DirFS("/"), paths)
}

// CheckWorkerStop checks the official worker prerequisites after local service
// teardown, for gateway and headless Apps alike. Registration-changing DSM
// operations must remain available to recover missing profiles.
func CheckWorkerStop(project, status string) error {
	switch status {
	case "INSTALL", "UPGRADE", "UNINSTALL":
		return nil
	}
	if err := CheckWorkerProfiles(project); err != nil {
		return fmt.Errorf("COMPOSE_STOP_UNCONFIRMED: Container Manager's project stop cannot be confirmed; use Package Center Manual Install with this App's same-version SPK while retaining App data: %w", err)
	}
	return nil
}

func loadWorkerProfilePaths(project string) ([]string, error) {
	if os.Getuid() == 0 || os.Getuid() != os.Geteuid() {
		return nil, problem("COMPOSE_ROOT_FORBIDDEN", "worker preparation must run as the package account")
	}
	f, err := os.OpenFile(filepath.Join(project, "compose.lock.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || !st.Mode().IsRegular() || st.Size() > 4<<20 || owner.Nlink != 1 {
		return nil, problem("COMPOSE_LOCK_INVALID", "invalid worker profile inventory")
	}
	return workerProfilePaths(io.LimitReader(f, 4<<20))
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
		err := checkWorkerProfiles(root, paths)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errWorkerProfilePending) {
			return err
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

var errWorkerProfilePending = errors.New("COMPOSE_WORKER_PROFILE_MISSING: this installed App has missing or empty Container Manager profiles; START cannot recreate them and STOP cannot be confirmed. In Package Center use Manual Install with this App's same-version SPK to rerun registration while retaining App data; Repair is not required. If the installer reports a duplicate project, use an administrator's synopkgland install <package> --repair with a CLI supporting owned-project recovery; never delete volumes or another App's project")

func checkWorkerProfiles(root fs.FS, paths []string) error {
	missing := false
	for _, path := range paths {
		// Stat does not open the DSM-owned private profile.
		st, err := fs.Stat(root, path)
		if errors.Is(err, fs.ErrNotExist) {
			missing = true
			continue
		}
		if err != nil || !st.Mode().IsRegular() {
			return problem("COMPOSE_WORKER_PROFILE_UNAVAILABLE", "cannot inspect this App's Container Manager profile; use Package Center Manual Install with this App's same-version SPK while retaining App data, then start it again")
		}
		missing = missing || st.Size() == 0
	}
	if missing {
		return errWorkerProfilePending
	}
	return nil
}
