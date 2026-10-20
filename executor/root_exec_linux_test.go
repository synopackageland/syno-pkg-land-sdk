//go:build linux

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRootExecChild is the executor's root-exec mode, run by rootLauncher.
func TestRootExecChild(t *testing.T) {
	args := os.Getenv("W3_ROOT_EXEC")
	if args == "" {
		t.Skip("child only")
	}
	os.Unsetenv("W3_ROOT_EXEC")
	err := runRootExec(strings.Split(args, "\x1f"))
	t.Fatal(err)
}

// rootLauncher is a runtime whose executor runs this test binary's root-exec.
func rootLauncher(t *testing.T) rootPrograms {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("root programs need root")
	}
	exe, _ := os.Executable()
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	os.Mkdir(filepath.Join(dir, "root"), 0700)
	launcher := filepath.Join(dir, "executor")
	script := "#!/bin/sh\nshift\nIFS=$(printf '\\037')\nexport W3_ROOT_EXEC=\"$*\"\nexec " + exe + " -test.run=^TestRootExecChild$\n"
	os.WriteFile(launcher, []byte(script), 0700)
	return rootPrograms{Dir: dir, Executable: launcher}
}

func pin(t *testing.T, r rootPrograms, name, kind string, content []byte, mode os.FileMode) RootEntry {
	t.Helper()
	p := r.path(name, kind)
	os.Remove(p)
	if err := os.WriteFile(p, content, mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, mode)
	digest, err := digestFile(context.Background(), p, maxExecutorBytes)
	if err != nil {
		t.Fatal(err)
	}
	return RootEntry{Digest: digest, Kind: kind, Run: &RootRun{}}
}

func runLinux(t *testing.T, r rootPrograms, c rootCall, timeout time.Duration) (int, string, error) {
	t.Helper()
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	code, err := runRoot(context.Background(), r, c, timeout, nil)
	return code, out.String(), err
}

func TestRootScriptGetsParamsOnStdinAndOnlyTheListedEnvironment(t *testing.T) {
	r := rootLauncher(t)
	os.Setenv("SECRET_FROM_EXECUTOR", "leak")
	defer os.Unsetenv("SECRET_FROM_EXECUTOR")
	entry := pin(t, r, "probe", "script", []byte("cat\necho\nenv | grep -v '^PWD=\\|^SHLVL=\\|^_=' | sort\necho args=\"$*\"\nexit 3\n"), 0600)
	code, out, err := runLinux(t, r, rootCall{Name: "probe", Entry: entry, Mode: "purge", Args: []string{"keep-state"}, Stdin: []byte(`{"a":1}`), Env: []string{"SYNOPKGLAND_PACKAGE=Demo", "SYNOPKGLAND_ROOT_MODE=purge"}}, 10*time.Second)
	want := "{\"a\":1}\nHOME=/\nLANG=C\nPATH=/usr/syno/bin:/usr/syno/sbin:/usr/bin:/bin\nSYNOPKGLAND_PACKAGE=Demo\nSYNOPKGLAND_ROOT_MODE=purge\nargs=keep-state\n"
	if err != nil || code != 3 || out != want {
		t.Fatalf("%d %v\n%q", code, err, out)
	}
}

func TestRootBinaryRunsFromItsVerifiedDescriptor(t *testing.T) {
	r := rootLauncher(t)
	cat, err := os.ReadFile("/bin/cat")
	if err != nil {
		t.Fatal(err)
	}
	entry := pin(t, r, "cat", "binary", cat, 0700)
	code, out, err := runLinux(t, r, rootCall{Name: "cat", Entry: entry, Mode: "run", Stdin: []byte(`{"x":"y"}`)}, 10*time.Second)
	if err != nil || code != 0 || out != `{"x":"y"}` {
		t.Fatal(code, out, err)
	}
	// The descriptor pins the inode the digest was checked on: a program
	// swapped in at the path afterwards is not what runs.
	f, err := openPinned(r.path("cat", "binary"), "binary", entry.Digest)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	swap := r.path("cat", "binary") + ".new"
	os.WriteFile(swap, []byte("#!/bin/sh\necho swapped\n"), 0700)
	os.Rename(swap, r.path("cat", "binary"))
	var pinned bytes.Buffer
	pinned.ReadFrom(f)
	if !bytes.Equal(pinned.Bytes(), cat) {
		t.Fatal("descriptor followed the swapped path")
	}
	if _, _, err = runLinux(t, r, rootCall{Name: "cat", Entry: entry, Mode: "run"}, 10*time.Second); err == nil {
		t.Fatal("the swapped program ran")
	}
}

func TestRootProgramsTheExecutorRefuses(t *testing.T) {
	r := rootLauncher(t)
	script := []byte("echo ran\n")
	for name, prepare := range map[string]func() RootEntry{
		"digest changed": func() RootEntry {
			e := pin(t, r, "p", "script", script, 0600)
			os.WriteFile(r.path("p", "script"), []byte("echo other\n"), 0600)
			return e
		},
		"group writable": func() RootEntry { return pin(t, r, "p", "script", script, 0620) },
		"world writable": func() RootEntry { return pin(t, r, "p", "script", script, 0602) },
		"not root's": func() RootEntry {
			e := pin(t, r, "p", "script", script, 0600)
			os.Chown(r.path("p", "script"), 1000, 1000)
			return e
		},
		"symlink": func() RootEntry {
			e := pin(t, r, "p", "script", script, 0600)
			os.Rename(r.path("p", "script"), filepath.Join(r.Dir, "real.sh"))
			os.Symlink(filepath.Join(r.Dir, "real.sh"), r.path("p", "script"))
			return e
		},
		"symlinked folder": func() RootEntry {
			e := pin(t, r, "p", "script", script, 0600)
			os.Rename(filepath.Join(r.Dir, "root"), filepath.Join(r.Dir, "elsewhere"))
			os.Symlink(filepath.Join(r.Dir, "elsewhere"), filepath.Join(r.Dir, "root"))
			return e
		},
		"binary not executable": func() RootEntry { return pin(t, r, "p", "binary", []byte("\x7fELF"), 0600) },
		"script pinned as binary": func() RootEntry {
			e := pin(t, r, "p", "script", script, 0700)
			e.Kind = "binary"
			return e
		},
	} {
		entry := prepare()
		if code, out, err := runLinux(t, r, rootCall{Name: "p", Entry: entry, Mode: "run"}, 10*time.Second); err == nil || strings.Contains(out, "ran") {
			t.Error(name, code, out)
		}
		if info, err := os.Lstat(filepath.Join(r.Dir, "root")); err == nil && info.Mode()&os.ModeSymlink != 0 {
			os.Remove(filepath.Join(r.Dir, "root"))
			os.Rename(filepath.Join(r.Dir, "elsewhere"), filepath.Join(r.Dir, "root"))
		}
		os.Remove(r.path("p", "script"))
		os.Remove(r.path("p", "binary"))
	}
	if _, err := r.Start(rootCall{Name: "../p", Entry: pin(t, r, "q", "script", script, 0600)}); err == nil {
		t.Fatal("path traversal")
	}
}

func TestRootProgramsRunNicedWithoutCoreDumpsInTheirOwnGroup(t *testing.T) {
	r := rootLauncher(t)
	entry := pin(t, r, "probe", "script", []byte("ulimit -c\ncut -d' ' -f19 /proc/self/stat\n[ \"$(cut -d' ' -f5 /proc/$$/stat)\" = $$ ] && echo leader\n"), 0600)
	nice := 19
	entry.Job = &RootJob{Nice: &nice}
	if _, out, err := runLinux(t, r, rootCall{Name: "probe", Entry: entry, Mode: "job"}, 10*time.Second); err != nil || out != "0\n19\nleader\n" {
		t.Fatalf("%v %q", err, out)
	}
	if _, out, _ := runLinux(t, r, rootCall{Name: "probe", Entry: entry, Mode: "run"}, 10*time.Second); out != "0\n0\nleader\n" {
		t.Fatalf("%q", out)
	}
}

func TestRootTimeoutKillsTheWholeProcessGroup(t *testing.T) {
	r := rootLauncher(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	entry := pin(t, r, "slow", "script", []byte("sleep 300 &\necho $! > "+pidFile+"\nsleep 300\n"), 0600)
	start := time.Now()
	if _, _, err := runLinux(t, r, rootCall{Name: "slow", Entry: entry, Mode: "run"}, time.Second); err != errRootTimeout || time.Since(start) > 10*time.Second {
		t.Fatal(err, time.Since(start))
	}
	b, _ := os.ReadFile(pidFile)
	pid := strings.TrimSpace(string(b))
	time.Sleep(100 * time.Millisecond)
	if stat, err := os.ReadFile("/proc/" + pid + "/stat"); err == nil && !strings.Contains(string(stat), ") Z ") {
		t.Fatal("background child survived", string(stat))
	}
}

func TestRootStagingFilesArePackageOwnedAndPrivate(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("chown needs root")
	}
	base := t.TempDir()
	stage := filepath.Join(base, "executor-staging")
	os.Mkdir(stage, 0700)
	os.Chown(stage, 1400, 1400)
	w := subprocessWriter{Staging: stage, PackageUID: 1400, PackageGID: 1400}
	f, name, err := w.Create()
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("output")
	f.Close()
	info, _ := os.Lstat(filepath.Join(stage, name))
	st := info.Sys().(*syscall.Stat_t)
	if !strings.HasPrefix(name, "root-") || st.Uid != 1400 || st.Gid != 1400 || info.Mode().Perm() != 0600 {
		t.Fatal(name, st.Uid, info.Mode())
	}
	os.WriteFile(filepath.Join(stage, "root-old"), make([]byte, 10), 0600)
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(filepath.Join(stage, "root-old"), old, old)
	os.Symlink("/etc/shadow", filepath.Join(stage, "root-link"))
	if used, err := w.Used(); err != nil || used != 6 {
		t.Fatal(used, err)
	}
	if _, err := os.Lstat(filepath.Join(stage, "root-old")); !os.IsNotExist(err) {
		t.Fatal("an unclaimed hour-old output remains")
	}
	w.Remove(name)
	if _, err := os.Lstat(filepath.Join(stage, name)); !os.IsNotExist(err) {
		t.Fatal("not removed")
	}
	os.RemoveAll(stage)
	os.Symlink(base, stage)
	if _, _, err := w.Create(); err == nil {
		t.Fatal("symlinked staging folder accepted")
	}
}

func TestRootRunCreatesAMissingStagingFolderForLargeOutput(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("chown needs root")
	}
	e, _, r, _ := rootSetup(t, `{"report":{"digest":"`+digestA+`","run":{}}}`)
	stage := filepath.Join(t.TempDir(), "executor-staging")
	e.RootStaging = subprocessWriter{Staging: stage, PackageUID: 1400, PackageGID: 1400}
	big := strings.Repeat("x", 100<<10)
	r.run = func(_ context.Context, c rootCall) (int, error) {
		io.WriteString(c.Stdout, big)
		return 0, nil
	}
	out := result(t, request(e, "root.run", map[string]any{"name": "report"}))
	name, _ := out["staging"].(string)
	if strings.Contains(name, "/") || out["size"] != float64(len(big)) {
		t.Fatal(out)
	}
	info, err := os.Lstat(stage)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0700 || st.Uid != 1400 || st.Gid != 1400 {
		t.Fatal(info.Mode(), st.Uid, st.Gid)
	}
	if b, err := os.ReadFile(filepath.Join(stage, name)); err != nil || string(b) != big {
		t.Fatal(err, len(b))
	}
}

func TestARootJobTalksOverItsControlAndProgressDescriptors(t *testing.T) {
	r := rootLauncher(t)
	script := []byte(`printf '{"percent":10,"phase":"start"}\n' >&5
read -r line <&4
printf '{"percent":100,"result":{"got":%s}}\n' "$line" >&5
`)
	entry := pin(t, r, "clean", "script", script, 0600)
	grace := 5
	entry.Run, entry.Job = nil, &RootJob{GraceS: &grace}
	e, _, _, _ := rootSetup(t, `{"clean":{"digest":"`+entry.Digest+`","job":{"grace_s":5}}}`)
	e.Runner = r
	if err := e.startJobs(); err != nil {
		t.Fatal(err)
	}
	defer e.stopJobs("executor-stop")
	id := startJob(t, e, nil)
	for {
		job := result(t, request(e, "root.jobs.get", map[string]any{"job_id": id}))
		if job["progress"] != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	request(e, "root.jobs.stop", map[string]any{"job_id": id})
	job := awaitJob(t, e, id, "canceled")
	if res, _ := job["result"].(map[string]any); res == nil || res["got"].(map[string]any)["cancel"] != "user" || job["exit_code"] != 0.0 {
		t.Fatal(job)
	}
}

func TestAJobAnEndedExecutorLeftRunningIsKilledAtTheNextStart(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root")
	}
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	start, err := processStart(pid)
	if err != nil {
		t.Fatal(err)
	}
	e, _, _, _ := rootSetup(t, `{"clean":{"digest":"`+digestA+`","job":{}}}`)
	alive := JobRecord{Version: 1, ID: strings.Repeat("d", 64), Name: "clean", State: "running", RequestedBy: "alice", StartedAt: e.Now(), Pid: pid, PidStart: start}
	// The same pid with another start time is another process: left alone.
	reused := JobRecord{Version: 1, ID: strings.Repeat("e", 64), Name: "clean", State: "stopping", RequestedBy: "alice", StartedAt: e.Now(), Pid: os.Getpid(), PidStart: "1"}
	e.Store.putJob(alive)
	e.Store.putJob(reused)
	if err := e.startJobs(); err != nil {
		t.Fatal(err)
	}
	defer e.stopJobs("executor-stop")
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the job's process group survived")
	}
	for _, id := range []string{alive.ID, reused.ID} {
		if rec, _ := e.Store.job(id); rec.State != "interrupted" {
			t.Fatal(rec)
		}
	}
}

func TestTheRootStateDirectoryIsRootsAlone(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root")
	}
	volume := "/volume97"
	if err := os.Mkdir(volume, 0755); err != nil {
		t.Skip(err)
	}
	defer os.RemoveAll(volume)
	state := volume + "/@synopkgland/Demo"
	if err := ensureRootState(state); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{volume + "/@synopkgland": 0755, state: 0700} {
		info, err := os.Lstat(path)
		if err != nil || !ownedByRoot(info) || info.Mode().Perm() != mode {
			t.Fatal(path, info.Mode(), err)
		}
	}
	if err := ensureRootState(state); err != nil {
		t.Fatal("not idempotent", err)
	}
	os.Chmod(state, 0777)
	if ensureRootState(state) == nil {
		t.Fatal("a writable state directory accepted")
	}
	os.RemoveAll(volume + "/@synopkgland")
	os.Symlink("/tmp", volume+"/@synopkgland")
	if ensureRootState(state) == nil {
		t.Fatal("a symlinked parent accepted")
	}
}
