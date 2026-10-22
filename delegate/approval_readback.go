package delegate

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"golang.org/x/sys/unix"
)

// Root's diagnostic file stays readable while the executor socket is paused.
// Never accept a file supplied by the App or from a configurable client.
var approvalDigestRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

func protectedApproval(c ExecutorClient) json.RawMessage {
	client, ok := c.(*Client)
	if !ok || client == nil {
		return nil
	}
	dir := filepath.Dir(client.Socket)
	pkg := filepath.Base(dir)
	if client.Socket != "/run/synopkgland/"+pkg+"/executor.sock" {
		return nil
	}
	return readProtectedApproval(dir, pkg)
}
func readProtectedApproval(dir, pkg string) json.RawMessage {
	parent, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	defer parent.Close()
	st, err := parent.Stat()
	if err != nil || !st.IsDir() || !rootReadOnly(st) {
		return nil
	}
	fd, err := unix.Openat(int(parent.Fd()), "approval.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	f := os.NewFile(uintptr(fd), "approval.json")
	defer f.Close()
	st, err = f.Stat()
	if err != nil || !st.Mode().IsRegular() || !rootReadOnly(st) || st.Size() > 128*1024 {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(f, 128*1024+1))
	if err != nil || len(b) > 128*1024 {
		return nil
	}
	var v struct {
		Package  string `json:"package"`
		Old      string `json:"old_policy_digest"`
		New      string `json:"new_policy_digest"`
		Executor string `json:"new_executor_digest"`
	}
	if json.Unmarshal(b, &v) != nil || v.Package != pkg || !approvalDigestRE.MatchString(v.Old) || !approvalDigestRE.MatchString(v.New) || !approvalDigestRE.MatchString(v.Executor) {
		return nil
	}
	return b
}
func rootReadOnly(st os.FileInfo) bool {
	raw, ok := st.Sys().(*syscall.Stat_t)
	return ok && raw.Uid == 0 && st.Mode().Perm()&0022 == 0
}
