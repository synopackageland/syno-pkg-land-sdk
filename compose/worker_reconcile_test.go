package compose

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type workerCM struct {
	project    WorkerProject
	containers map[string]WorkerContainer
	profiles   map[string]string
	ops        []string
	fail       bool
	change     bool
	reads      int
}

func (c *workerCM) Project(context.Context, string) (WorkerProject, error) {
	c.reads++
	p := c.project
	if c.change && c.reads > 1 {
		p.Package = "OtherApp"
	}
	return p, nil
}
func (c *workerCM) Container(_ context.Context, id string) (WorkerContainer, error) {
	v, ok := c.containers[id]
	if !ok {
		return v, errors.New("absent")
	}
	return v, nil
}
func (c *workerCM) ProfileContainerID(_ context.Context, name string) (string, error) {
	return c.profiles[name], nil
}
func (c *workerCM) Stop(_ context.Context, name string) error {
	c.ops = append(c.ops, "stop:"+name)
	if c.fail {
		return errors.New("private backend log")
	}
	for id, v := range c.containers {
		if v.Name == name {
			v.State = "exited"
			c.containers[id] = v
			c.profiles[name] = id
		}
	}
	return nil
}
func (c *workerCM) Delete(_ context.Context, name string) error {
	c.ops = append(c.ops, "delete:"+name)
	if c.fail {
		return errors.New("private backend log")
	}
	for id, v := range c.containers {
		if v.Name == name {
			delete(c.containers, id)
			delete(c.profiles, name)
			c.project.ContainerIDs = slices.DeleteFunc(c.project.ContainerIDs, func(value string) bool { return value == id })
		}
	}
	return nil
}
func workerFixture() (WorkerOwner, *workerCM) {
	o := WorkerOwner{Package: "ExampleApp", Project: "spl-exampleapp", Path: "/var/packages/ExampleApp/target/compose", Services: []string{"web"}}
	id := strings.Repeat("a", 64)
	v := WorkerContainer{ID: id, Name: o.Project + "-web-1", Project: o.Project, Service: "web", WorkingDir: o.Path, ConfigFiles: o.Path + "/docker-compose.yml", State: "exited"}
	c := &workerCM{project: WorkerProject{ID: "project-id", Name: o.Project, Package: o.Package, Path: o.Path, IsPackage: true, ContainerIDs: []string{id}}, containers: map[string]WorkerContainer{id: v}, profiles: map[string]string{}}
	return o, c
}
func TestReconcileMissingProfileViaOfficialContainerStop(t *testing.T) {
	o, c := workerFixture()
	id := c.project.ContainerIDs[0]
	v := c.containers[id]
	v.State = "running"
	c.containers[id] = v
	if err := ReconcileWorkerContainers(context.Background(), c, "project-id", o, RefreshWorkerProfiles); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.ops, []string{"stop:spl-exampleapp-web-1"}) || c.profiles[v.Name] != id || c.containers[id].State != "exited" {
		t.Fatalf("not repaired: %+v", c)
	}
	c.ops = nil
	if err := ReconcileWorkerContainers(context.Background(), c, "project-id", o, RefreshWorkerProfiles); err != nil || len(c.ops) != 0 {
		t.Fatal("healthy profile was changed", err, c.ops)
	}
}
func TestRemoveStoppedContainersBeforeWorkerReplacement(t *testing.T) {
	o, c := workerFixture()
	if err := ReconcileWorkerContainers(context.Background(), c, "project-id", o, RemoveStoppedWorkerContainers); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.ops, []string{"delete:spl-exampleapp-web-1"}) || len(c.containers) != 0 {
		t.Fatal(c.ops)
	}
}

func TestWorkerReconcileAcceptsCMGeneratedAdminComposeFile(t *testing.T) {
	o, c := workerFixture()
	for id, v := range c.containers {
		v.ConfigFiles = o.Path + "/docker-compose.admin.yaml"
		c.containers[id] = v
	}
	if err := ReconcileWorkerContainers(context.Background(), c, "project-id", o, RemoveStoppedWorkerContainers); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerReconcileRemovesEveryVerifiedStoppedService(t *testing.T) {
	o, c := workerFixture()
	o.Services = append(o.Services, "cache")
	id := strings.Repeat("b", 64)
	v := c.containers[c.project.ContainerIDs[0]]
	v.ID = id
	v.Service = "cache"
	v.Name = o.Project + "-cache-1"
	c.containers[id] = v
	c.project.ContainerIDs = append(c.project.ContainerIDs, id)
	if err := ReconcileWorkerContainers(context.Background(), c, "project-id", o, RemoveStoppedWorkerContainers); err != nil {
		t.Fatal(err)
	}
	if len(c.ops) != 2 || len(c.containers) != 0 || len(c.project.ContainerIDs) != 0 {
		t.Fatal("incomplete service cleanup", c.ops)
	}
}

func TestWorkerReconcileChecksStoredProfileIDAndUnknownContainerState(t *testing.T) {
	o, c := workerFixture()
	name := o.Project + "-web-1"
	c.profiles[name] = strings.Repeat("b", 64)
	if err := ReconcileWorkerContainers(context.Background(), c, "project-id", o, RefreshWorkerProfiles); err != nil || len(c.ops) != 1 {
		t.Fatal("stale profile was trusted", err, c.ops)
	}
	o, c = workerFixture()
	for id, v := range c.containers {
		v.State = ""
		c.containers[id] = v
	}
	if err := ReconcileWorkerContainers(context.Background(), c, "project-id", o, RemoveStoppedWorkerContainers); err == nil || len(c.ops) != 0 {
		t.Fatal("unknown state was deleted", err, c.ops)
	}
}
func TestWorkerReconcileRefusesUnownedOrLiveDeletionBeforeMutation(t *testing.T) {
	for _, edit := range []func(*WorkerOwner, *workerCM){
		func(o *WorkerOwner, c *workerCM) { c.project.Package = "OtherApp" },
		func(o *WorkerOwner, c *workerCM) { c.project.Path = "/volume1/foreign" },
		func(o *WorkerOwner, c *workerCM) { c.project.IsPackage = false },
		func(o *WorkerOwner, c *workerCM) { c.project.ID = "other-id" },
		func(o *WorkerOwner, c *workerCM) { o.Services = []string{"other"} },
		func(o *WorkerOwner, c *workerCM) {
			for id, v := range c.containers {
				v.State = "running"
				c.containers[id] = v
			}
		},
		func(o *WorkerOwner, c *workerCM) {
			for id, v := range c.containers {
				v.WorkingDir = "/foreign"
				c.containers[id] = v
			}
		},
		func(o *WorkerOwner, c *workerCM) {
			for id, v := range c.containers {
				v.ConfigFiles = "/foreign/docker-compose.yml"
				c.containers[id] = v
			}
		},
		func(o *WorkerOwner, c *workerCM) {
			for id, v := range c.containers {
				v.Name = "foreign"
				c.containers[id] = v
			}
		},
		func(o *WorkerOwner, c *workerCM) {
			c.project.ContainerIDs = append(c.project.ContainerIDs, strings.Repeat("b", 64))
		},
	} {
		o, c := workerFixture()
		edit(&o, c)
		if err := ReconcileWorkerContainers(context.Background(), c, "project-id", o, RemoveStoppedWorkerContainers); err == nil || len(c.ops) != 0 {
			t.Fatalf("unsafe mutation: %v %v", err, c.ops)
		}
	}
}
func TestWorkerReconcileRechecksOwnerAndDoesNotRetryUnknownWrites(t *testing.T) {
	for _, changed := range []bool{true, false} {
		o, c := workerFixture()
		c.change = changed
		c.fail = !changed
		err := ReconcileWorkerContainers(context.Background(), c, "project-id", o, RefreshWorkerProfiles)
		if err == nil || strings.Contains(err.Error(), "private backend") {
			t.Fatal(err)
		}
		want := 1
		if changed {
			want = 0
		}
		if len(c.ops) != want {
			t.Fatal(c.ops)
		}
	}
	o, c := workerFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ReconcileWorkerContainers(ctx, c, "project-id", o, RefreshWorkerProfiles); !errors.Is(err, context.Canceled) || len(c.ops) != 0 {
		t.Fatal(err, c.ops)
	}
}
