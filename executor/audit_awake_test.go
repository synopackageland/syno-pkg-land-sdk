package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuditDiskAwakeProbe(t *testing.T) {
	for _, c := range []struct {
		name, spin, idle, rotational string
		want                         bool
	}{
		{"awake", "0", "60", "1", true},
		{"asleep", "1", "1200", "1", false},
		{"recent idle fallback", "", "1", "1", true},
		{"old idle is not awake proof", "", "60", "1", false},
		{"invalid idle zero", "", "0", "1", false},
		{"missing indicators", "", "", "1", false},
		{"malformed indicator", "unknown", "60", "1", false},
		{"SSD has no spin hibernation", "", "", "0", true},
		{"unknown platform", "", "", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			sys := t.TempDir()
			disk := filepath.Join(sys, "devices", "block", "sda")
			part := filepath.Join(disk, "sda1")
			slaves := filepath.Join(sys, "class", "block", "md0", "slaves")
			for _, dir := range []string{part, slaves, filepath.Join(disk, "device"), filepath.Join(disk, "queue")} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(part, "partition"), []byte("1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(part, filepath.Join(slaves, "sda1")); err != nil {
				t.Fatal(err)
			}
			for path, value := range map[string]string{
				"device/syno_spindown":  c.spin,
				"device/syno_idle_time": c.idle,
				"queue/rotational":      c.rotational,
			} {
				if value != "" {
					if err := os.WriteFile(filepath.Join(disk, path), []byte(value+"\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if got := auditDisksAwake(sys); got != c.want {
				t.Fatalf("awake = %v, want %v", got, c.want)
			}
			// Every member of md0 must be known awake. A second member whose
			// state is unknown cannot be hidden by an awake first member.
			other := filepath.Join(sys, "devices", "block", "sdb")
			if err := os.MkdirAll(other, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(slaves, "sdb")); err != nil {
				t.Fatal(err)
			}
			if auditDisksAwake(sys) {
				t.Fatal("unknown RAID member treated as awake")
			}
		})
	}
	if auditDisksAwake(t.TempDir()) {
		t.Fatal("missing md0 treated as awake")
	}
}
