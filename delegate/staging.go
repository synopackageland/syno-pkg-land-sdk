package delegate

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

func validStaging(p string) bool {
	if p == "" || len(p) > 4096 || path.IsAbs(p) || path.Clean(p) != p {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if !validName(part) {
			return false
		}
	}
	return true
}

// stagingParent opens each relative directory without following symlinks.
// The startup-owned staging root itself must be private and package-owned.
func stagingParent(stagingDir, relative string) (int, background.StagingIdentity, error) {
	var identity background.StagingIdentity
	if !validStaging(relative) || background.RefuseRoot(os.Geteuid()) != nil {
		return -1, identity, fail("STAGING_UNAVAILABLE")
	}
	fd, err := unix.Open(stagingDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, identity, fail("STAGING_UNAVAILABLE")
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 {
		unix.Close(fd)
		return -1, identity, fail("STAGING_UNAVAILABLE")
	}
	identity.RootDevice, identity.RootInode = uint64(st.Dev), uint64(st.Ino)
	parts := strings.Split(relative, "/")
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return -1, identity, fail("STAGING_UNAVAILABLE")
		}
		fd = next
		if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
			unix.Close(fd)
			return -1, identity, fail("STAGING_UNAVAILABLE")
		}
	}
	if unix.Fstat(fd, &st) != nil {
		unix.Close(fd)
		return -1, identity, fail("STAGING_UNAVAILABLE")
	}
	identity.ParentDevice, identity.ParentInode = uint64(st.Dev), uint64(st.Ino)
	return fd, identity, nil
}

func sourceIdentity(parent int, relative string, identity background.StagingIdentity) (background.StagingIdentity, error) {
	fd, err := unix.Openat(parent, path.Base(relative), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return identity, err
	}
	file := os.NewFile(uintptr(fd), "staging")
	defer file.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		return identity, fail("STAGING_UNAVAILABLE")
	}
	info, err := file.Stat()
	if err != nil {
		return identity, err
	}
	identity.Device, identity.Inode, identity.UID, identity.Size, identity.ModifiedNS = uint64(st.Dev), uint64(st.Ino), st.Uid, st.Size, info.ModTime().UnixNano()
	return identity, nil
}

func pinSource(stagingDir, relative string, size int64) (background.StagingIdentity, error) {
	parent, identity, err := stagingParent(stagingDir, relative)
	if err != nil {
		return identity, err
	}
	defer unix.Close(parent)
	identity, err = sourceIdentity(parent, relative, identity)
	if err != nil || identity.Size != size {
		return identity, fail("STAGING_UNAVAILABLE")
	}
	return identity, nil
}

// cleanupSource runs under the package UID, never the destination subject or
// root. A replaced source/parent is retained; only the pinned name is removed.
// As with the executor's FD design, a concurrent rename after the last check
// is not an ancestry sandbox or an atomic compare-and-unlink primitive.
func cleanupSource(stagingDir, relative string, pinned background.StagingIdentity) error {
	parent, identity, err := stagingParent(stagingDir, relative)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	if identity.RootDevice != pinned.RootDevice || identity.RootInode != pinned.RootInode || identity.ParentDevice != pinned.ParentDevice || identity.ParentInode != pinned.ParentInode {
		return fail("STAGING_CHANGED")
	}
	identity, err = sourceIdentity(parent, relative, identity)
	if err == unix.ENOENT {
		removeEmptyFolders(stagingDir, relative)
		return nil
	}
	if err != nil || identity != pinned {
		return fail("STAGING_CHANGED")
	}
	if err = unix.Unlinkat(parent, path.Base(relative), 0); err != nil {
		return fail("STAGING_UNAVAILABLE")
	}
	if err = unix.Fsync(parent); err != nil {
		return fail("STAGING_UNAVAILABLE")
	}
	removeEmptyFolders(stagingDir, relative)
	return nil
}

// removeEmptyFolders removes the folders of relative its removed source left
// empty, deepest first, never the staging root. A folder that holds anything
// else ends the walk, so an App creates its staging folder for each job.
func removeEmptyFolders(stagingDir, relative string) {
	parts := strings.Split(relative, "/")
	for k := len(parts) - 1; k > 0; k-- {
		parent, _, err := stagingParent(stagingDir, strings.Join(parts[:k], "/"))
		if err != nil {
			return
		}
		err = unix.Unlinkat(parent, parts[k-1], unix.AT_REMOVEDIR)
		if err == nil {
			err = unix.Fsync(parent)
		}
		unix.Close(parent)
		if err != nil && err != unix.ENOENT {
			return
		}
	}
}

func ownerNamespace(user, grant string) string {
	sum := sha256.Sum256([]byte(user + "\x00" + grant))
	return hex.EncodeToString(sum[:])
}

// OwnerStagingPrefix names a producer namespace for an authenticated account
// and its grant. A recreated account cannot reuse the old grant. The prefix
// is mandatory on HTTP/bridge admission; the trusted daemon facade is separate.
func OwnerStagingPrefix(user, grant string) string {
	return "owners/" + ownerNamespace(user, grant) + "/"
}

// OwnerJobSuffix binds an HTTP/bridge producer job ID to its authenticated
// account and grant. Append it to a producer-local ID of at most 95 bytes.
func OwnerJobSuffix(user, grant string) string {
	return "-" + ownerNamespace(user, grant)[:32]
}
