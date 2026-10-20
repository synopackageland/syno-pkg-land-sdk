package containerpreflight

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func fixture() fstest.MapFS {
	return fstest.MapFS{
		"var/packages/ContainerManager/INFO":                  &fstest.MapFile{Data: []byte("package=\"ContainerManager\"\n")},
		"var/packages/ContainerManager/target/usr/bin/docker": &fstest.MapFile{Mode: 0755},
		"proc/net/unix": &fstest.MapFile{Data: []byte("Num RefCount Protocol Flags Type St Inode Path\n00: 2 0 00010000 0001 01 99 /var/run/docker.sock\n")},
	}
}
func TestAvailability(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(fstest.MapFS)
		code   string
	}{
		{"ready", func(fstest.MapFS) {}, ""},
		{"missing", func(f fstest.MapFS) { delete(f, "var/packages/ContainerManager/INFO") }, "COMPOSE_CONTAINER_MANAGER_REQUIRED"},
		{"missingDocker", func(f fstest.MapFS) { delete(f, "var/packages/ContainerManager/target/usr/bin/docker") }, "COMPOSE_CONTAINER_MANAGER_NOT_READY"},
		{"notExecutable", func(f fstest.MapFS) { f["var/packages/ContainerManager/target/usr/bin/docker"].Mode = 0644 }, "COMPOSE_CONTAINER_MANAGER_NOT_READY"},
		{"stopped", func(f fstest.MapFS) { f["proc/net/unix"].Data = []byte("header\n") }, "COMPOSE_CONTAINER_MANAGER_NOT_READY"},
		{"unrelatedSocket", func(f fstest.MapFS) {
			f["proc/net/unix"].Data = []byte("00: 2 0 00010000 0001 01 99 /tmp/docker.sock\n")
		}, "COMPOSE_CONTAINER_MANAGER_NOT_READY"},
		{"staleSocket", func(f fstest.MapFS) {
			f["proc/net/unix"].Data = []byte("00: 2 0 00000000 0001 01 99 /var/run/docker.sock\n")
		}, "COMPOSE_CONTAINER_MANAGER_NOT_READY"},
		{"inventoryUnreadable", func(f fstest.MapFS) { delete(f, "proc/net/unix") }, "COMPOSE_CONTAINER_MANAGER_NOT_READY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixture()
			tc.change(f)
			err := check(f)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var p *Failure
			if !errors.As(err, &p) || p.Code != tc.code || p.MessageKey == "" || !strings.Contains(p.Message, "Package Center") {
				t.Fatalf("%+v", err)
			}
		})
	}
}

type unreadable struct{ fs.FS }

func (unreadable) Open(string) (fs.File, error) { return nil, fs.ErrPermission }
func TestUnreadableDoesNotClaimMissing(t *testing.T) {
	err := check(unreadable{})
	var p *Failure
	if !errors.As(err, &p) || p.Code != "COMPOSE_CONTAINER_MANAGER_NOT_READY" {
		t.Fatal(err)
	}
}
