package sharedfolder

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

var ErrUnsupportedStorage = errors.New("selected folder storage is unsupported")

// Mount metadata is readable without the administrator's session or root.
// This rejects known remote/read-only mounts, not a complete DSM share taxonomy.
func validateSelectedMount(path string) error {
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return fmt.Errorf("cannot read folder mount metadata: %w", err)
	}
	return validateMountInfo(path, string(raw))
}
func validateMountInfo(path, raw string) error {
	longest := -1
	kind, options := "", ""
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\134`, `\`)
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		mount := unescape.Replace(f[4])
		if mount != "/" && path != mount && !strings.HasPrefix(path, mount+"/") {
			continue
		}
		sep := -1
		for i := 6; i < len(f); i++ {
			if f[i] == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+1 >= len(f) || len(mount) <= longest {
			continue
		}
		longest = len(mount)
		kind = f[sep+1]
		options = f[5]
	}
	if longest < 0 {
		return fmt.Errorf("folder mount metadata unavailable")
	}
	switch kind {
	case "nfs", "nfs4", "cifs", "smb3", "ceph", "glusterfs", "fuse.sshfs", "fuse.rclone", "fuse.glusterfs":
		return fmt.Errorf("%w: %s", ErrUnsupportedStorage, kind)
	}
	for _, option := range strings.Split(options, ",") {
		if option == "ro" {
			return fmt.Errorf("%w: read-only mount", ErrUnsupportedStorage)
		}
	}
	return nil
}
