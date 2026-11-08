package compose

import (
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// WorkerOwner is trusted installed-package evidence, never browser input or an
// incoming SPK's claim. Path is the exact Compose working directory attested by
// DSM's package target. Adapters must resolve DSM-owned target symlinks before
// comparing aliases; do not clean an arbitrary path into an ownership match.
type WorkerOwner struct {
	Package, Project, Path string
	Services               []string
}

type WorkerProject struct {
	ID, Name, Package, Path string
	IsPackage               bool
	ContainerIDs            []string
}

// WorkerContainer contains fresh inspection evidence for the full Docker ID.
// Project, Service, WorkingDir and ConfigFiles are the com.docker.compose labels.
type WorkerContainer struct {
	ID, Name, Project, Service, WorkingDir, ConfigFiles string
	State                                               string
}

// WorkerContainerManager is an administrator adapter. Apps must not implement
// it using root hooks, Docker sockets, or credentials in their runtime. The
// caller holds its package lifecycle lock throughout reconciliation and the
// subsequent install/repair/uninstall; concurrent CM edits must be excluded.
// Reads must fail on unavailable/ambiguous evidence, including Project get 2105.
// Stop uses SYNO.Docker.Container stop by exact name; Delete uses that API's
// delete with force=false and preserve_profile=false. Neither may remove images,
// volumes, networks or project registrations. ProfileContainerID returns only the stored ID (empty when absent), rejecting
// symlinks/nonregular/untrusted files and never exposing other profile fields.
type WorkerContainerManager interface {
	Project(context.Context, string) (WorkerProject, error)
	Container(context.Context, string) (WorkerContainer, error)
	ProfileContainerID(context.Context, string) (string, error)
	Stop(context.Context, string) error
	Delete(context.Context, string) error
}

type WorkerReconcileAction int

const (
	// RefreshWorkerProfiles regenerates missing profiles through Container stop.
	// It can stop affected containers; the administrator then repairs/restarts
	// this package once. It never retries an uncertain write or runs compose up.
	RefreshWorkerProfiles WorkerReconcileAction = iota
	// RemoveStoppedWorkerContainers runs after package stop, before upgrade or
	// uninstall. Removing old containers and profiles together avoids Compose's
	// temporary-name recreation and CM's Down-before-profile-cleanup ordering.
	// Writable container layers are discarded, as on normal replacement; declared
	// named volumes and bind data are retained. Running containers are refused.
	RemoveStoppedWorkerContainers
)

var workerContainerID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var workerService = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// ReconcileWorkerContainers supplies the shared SDK algorithm for administrative
// CLI integration. It is deliberately not called by package-account preflight:
// DSM acquires its privileged worker after that hook returns. An SDK update
// alone cannot repair worker state or prevent recreation in already-built SPKs.
func ReconcileWorkerContainers(ctx context.Context, cm WorkerContainerManager, id string, owner WorkerOwner, action WorkerReconcileAction) error {
	refuse := func() error {
		return problem("COMPOSE_WORKER_OWNERSHIP_UNVERIFIED", "cannot verify this App's project and containers; no further action was taken")
	}
	if cm == nil || id == "" || !packageName.MatchString(owner.Package) || !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`).MatchString(owner.Project) || !filepath.IsAbs(owner.Path) || filepath.Clean(owner.Path) != owner.Path || strings.ContainsAny(owner.Path, "\x00\r\n,") || len(owner.Services) == 0 || len(owner.Services) > 64 || (action != RefreshWorkerProfiles && action != RemoveStoppedWorkerContainers) {
		return refuse()
	}
	seen := map[string]bool{}
	for _, service := range owner.Services {
		if !workerService.MatchString(service) || seen[service] {
			return refuse()
		}
		seen[service] = true
	}
	read := func() (WorkerProject, error) {
		if err := ctx.Err(); err != nil {
			return WorkerProject{}, err
		}
		p, err := cm.Project(ctx, id)
		if err != nil || p.ID != id || !p.IsPackage || p.Package != owner.Package || p.Name != owner.Project || p.Path != owner.Path || len(p.ContainerIDs) > len(owner.Services) {
			return p, refuse()
		}
		return p, nil
	}
	inspect := func(id string) (WorkerContainer, error) {
		if !workerContainerID.MatchString(id) {
			return WorkerContainer{}, refuse()
		}
		v, err := cm.Container(ctx, id)
		// CM materializes docker-compose.admin.yaml in the same owned directory
		// before invoking Compose. Both fixed filenames are observed contracts;
		// arbitrary config paths, lists and aliases are refused.
		configOwned := v.ConfigFiles == filepath.Join(owner.Path, "docker-compose.yml") || v.ConfigFiles == filepath.Join(owner.Path, "docker-compose.admin.yaml")
		if err != nil || v.ID != id || v.Project != owner.Project || !seen[v.Service] || v.Name != owner.Project+"-"+v.Service+"-1" || v.WorkingDir != owner.Path || !configOwned || (v.State != "running" && v.State != "paused" && v.State != "restarting" && v.State != "exited" && v.State != "created") || action == RemoveStoppedWorkerContainers && v.State != "exited" && v.State != "created" {
			return v, refuse()
		}
		return v, nil
	}
	p, err := read()
	if err != nil {
		return err
	}
	// Validate the entire inventory before the first mutation.
	var containers []WorkerContainer
	ids := map[string]bool{}
	names := map[string]bool{}
	for _, id := range p.ContainerIDs {
		v, err := inspect(id)
		if err != nil || ids[id] || names[v.Name] {
			return refuse()
		}
		ids[id] = true
		names[v.Name] = true
		containers = append(containers, v)
	}
	for _, v := range containers {
		p, err = read()
		if err != nil {
			return err
		}
		if !slices.Contains(p.ContainerIDs, v.ID) {
			return refuse()
		}
		current, err := inspect(v.ID)
		if err != nil || current.Name != v.Name {
			return refuse()
		}
		if action == RefreshWorkerProfiles {
			profileID, e := cm.ProfileContainerID(ctx, v.Name)
			if e != nil {
				return refuse()
			}
			if profileID == v.ID {
				continue
			}
			err = cm.Stop(ctx, v.Name)
		} else {
			err = cm.Delete(ctx, v.Name)
		}
		if err != nil {
			return problem("COMPOSE_WORKER_RECONCILE_UNCONFIRMED", "Container Manager action is unconfirmed; inspect this App before retrying")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if action == RefreshWorkerProfiles {
			after, e := inspect(v.ID)
			if e != nil || after.State != "exited" && after.State != "created" {
				return refuse()
			}
			profileID, e := cm.ProfileContainerID(ctx, v.Name)
			if e != nil || profileID != v.ID {
				return problem("COMPOSE_WORKER_PROFILE_UNAVAILABLE", "Container Manager did not restore this App's profile; repair requires administrator support")
			}
		} else {
			after, e := read()
			if e != nil {
				return e
			}
			if slices.Contains(after.ContainerIDs, v.ID) {
				return problem("COMPOSE_WORKER_RECONCILE_UNCONFIRMED", "Container Manager still reports the removed container; inspect this App before retrying")
			}
			profileID, e := cm.ProfileContainerID(ctx, v.Name)
			if e != nil || profileID != "" {
				return problem("COMPOSE_WORKER_RECONCILE_UNCONFIRMED", "Container Manager profile cleanup is unconfirmed; inspect this App before retrying")
			}
		}
	}
	return nil
}
