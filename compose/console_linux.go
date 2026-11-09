package compose

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// NSSWrapperLibraries are the image paths the init looks for libnss_wrapper.
var NSSWrapperLibraries = []string{
	"/usr/local/lib/libnss_wrapper.so",
	"/usr/lib/x86_64-linux-gnu/libnss_wrapper.so",
	"/usr/lib/aarch64-linux-gnu/libnss_wrapper.so",
	"/usr/lib/arm-linux-gnueabihf/libnss_wrapper.so",
	"/usr/lib64/libnss_wrapper.so",
	"/usr/lib/libnss_wrapper.so",
}

// Account databases and sudo locations; variables only for tests.
var (
	etcPasswd = "/etc/passwd"
	etcGroup  = "/etc/group"
	sudoPaths = []string{"/usr/bin/sudo", "/bin/sudo", "/usr/local/bin/sudo"}
)

// ConsoleEnv is the environment the init adds for the application argv.
type ConsoleEnv map[string]string

// InitializeConsole prepares identity, HOME, TMPDIR and credential modes as the
// package UID. It never chowns, never runs as root and never creates
// credential paths. Notes are single log lines for the container log.
func InitializeConsole(ctx context.Context, c ConsoleConfig, logf func(string)) (ConsoleEnv, error) {
	uid, gid := os.Geteuid(), os.Getegid()
	if uid == 0 || os.Getuid() != uid {
		return nil, problem("COMPOSE_CONSOLE_INVALID", "the console init runs as the package UID, not root")
	}
	if !containerPath(c.Home) || !consoleUser.MatchString(c.User) || c.Tmp != "" && !containerPath(c.Tmp) {
		return nil, problem("COMPOSE_CONSOLE_INVALID", "invalid console declaration")
	}
	env := ConsoleEnv{"HOME": c.Home, "USER": c.User, "LOGNAME": c.User}
	if err := privateDirectory(c.Home, false, logf); err != nil {
		return nil, problem("COMPOSE_CONSOLE_HOME_UNAVAILABLE", "%s: %v", c.Home, err)
	}
	if c.Tmp != "" {
		if err := privateDirectory(c.Tmp, true, logf); err != nil {
			return nil, problem("COMPOSE_CONSOLE_TMP_UNAVAILABLE", "%s: %v", c.Tmp, err)
		}
		env["TMPDIR"] = c.Tmp
	}
	shell := "/bin/sh"
	if st, err := os.Stat("/bin/bash"); err == nil && st.Mode()&0111 != 0 {
		shell = "/bin/bash"
	}
	entry := fmt.Sprintf("%s:*:%d:%d:%s:%s:%s", c.User, uid, gid, c.User, c.Home, shell)
	if err := identity(c, uid, gid, entry, env, logf); err != nil {
		return nil, err
	}
	FixPermissions(c.Home, logf)
	if pathEnv := os.Getenv("PATH"); pathEnv == "" {
		env["PATH"] = ConsoleBin + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	} else if !slicesContains(strings.Split(pathEnv, ":"), ConsoleBin) {
		env["PATH"] = pathEnv + ":" + ConsoleBin
	}
	if c.Sudo {
		if err := checkSudo(ctx, env); err != nil {
			return nil, err
		}
	}
	return env, nil
}

func slicesContains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func hasAccount(file string, field int, id int) bool {
	f, err := os.Open(file)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), ":")
		if len(parts) > field && parts[field] == strconv.Itoa(id) {
			return true
		}
	}
	return false
}

// identity gives the package UID an account. sudo is setuid, so it ignores
// LD_PRELOAD/nss_wrapper and needs a real /etc/passwd entry; the sudo image
// contract makes /etc/passwd and /etc/group appendable for that reason.
func identity(c ConsoleConfig, uid, gid int, entry string, env ConsoleEnv, logf func(string)) error {
	if hasAccount(etcPasswd, 2, uid) {
		return nil
	}
	if c.Sudo {
		if err := appendLine(etcPasswd, entry); err != nil {
			return problem("COMPOSE_CONSOLE_SUDO_UNAVAILABLE", "cannot add the package account to /etc/passwd (%v); a sudo image must let the package user append /etc/passwd and /etc/group", err)
		}
		if !hasAccount(etcGroup, 2, gid) {
			if err := appendLine(etcGroup, fmt.Sprintf("%s:x:%d:", c.User, gid)); err != nil {
				return problem("COMPOSE_CONSOLE_SUDO_UNAVAILABLE", "cannot add the package group to /etc/group (%v)", err)
			}
		}
		return nil
	}
	lib := ""
	for _, candidate := range NSSWrapperLibraries {
		if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
			lib = candidate
			break
		}
	}
	if lib == "" {
		logf(fmt.Sprintf("synopkgland: no account entry for UID %d: the image has no libnss_wrapper; tools that look up the user (ssh, git) may fail", uid))
		return nil
	}
	base := env["TMPDIR"]
	if base == "" {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "synopkgland-identity-")
	if err != nil {
		return problem("COMPOSE_CONSOLE_TMP_UNAVAILABLE", "identity files: %v", err)
	}
	passwd, _ := os.ReadFile(etcPasswd)
	group, _ := os.ReadFile(etcGroup)
	var rows []string
	for _, row := range strings.Split(strings.TrimSpace(string(passwd)), "\n") {
		if f := strings.Split(row, ":"); row != "" && !(len(f) == 7 && f[0] == c.User) {
			rows = append(rows, row)
		}
	}
	rows = append(rows, entry)
	if !hasAccount(etcGroup, 2, gid) {
		group = append(bytes.TrimRight(group, "\n"), []byte(fmt.Sprintf("\n%s:x:%d:\n", c.User, gid))...)
	}
	if err := os.WriteFile(path.Join(dir, "passwd"), []byte(strings.Join(rows, "\n")+"\n"), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(path.Join(dir, "group"), group, 0600); err != nil {
		return err
	}
	preload := lib
	if old := os.Getenv("LD_PRELOAD"); old != "" {
		preload = lib + ":" + old
	}
	env["LD_PRELOAD"], env["NSS_WRAPPER_PASSWD"], env["NSS_WRAPPER_GROUP"] = preload, path.Join(dir, "passwd"), path.Join(dir, "group")
	return nil
}

func appendLine(file, line string) error {
	fd, err := unix.Open(file, unix.O_WRONLY|unix.O_APPEND|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	_, err = unix.Write(fd, []byte(line+"\n"))
	return err
}

func checkSudo(ctx context.Context, env ConsoleEnv) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	sudo := ""
	for _, p := range sudoPaths {
		if st, err := os.Stat(p); err == nil && st.Mode()&os.ModeSetuid != 0 {
			sudo = p
			break
		}
	}
	if sudo == "" {
		return problem("COMPOSE_CONSOLE_SUDO_UNAVAILABLE", "run_as.sudo is declared but the image has no setuid sudo; install sudo in the image or remove the opt-in")
	}
	cmd := exec.CommandContext(ctx, sudo, "-n", "true")
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
		return problem("COMPOSE_CONSOLE_SUDO_UNAVAILABLE", "sudo -n true failed (%s); the image needs a NOPASSWD sudoers rule for the console user", line)
	}
	return nil
}

// privateDirectory makes the last component (if missing) with mode 0700 and
// checks it is a real directory. With tighten (TMPDIR) it must belong to the
// UID and loses group/other bits. HOME keeps its mode and may be a selected
// folder owned by another account; that is logged, not changed.
func privateDirectory(p string, tighten bool, logf func(string)) error {
	if err := os.Mkdir(p, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	fd, err := unix.Open(p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if int(st.Uid) != os.Geteuid() {
		if tighten {
			return fmt.Errorf("owned by UID %d, not the package UID", st.Uid)
		}
		logf(fmt.Sprintf("synopkgland: HOME %s is owned by UID %d, not the package UID %d; it is used as is", p, st.Uid, os.Geteuid()))
	}
	if tighten && st.Mode&0077 != 0 {
		if err := unix.Fchmod(fd, st.Mode&0700); err != nil {
			return err
		}
	}
	return nil
}

// FixPermissions clears group/other mode bits on the credential paths in
// SensitivePaths that already exist below home and belong to the current UID.
// It never follows symlinks, never creates paths and never touches another
// account's files (one log line instead). It returns the paths it changed.
func FixPermissions(home string, logf func(string)) []string {
	return fixPermissions(home, uint32(os.Geteuid()), logf)
}

func fixPermissions(home string, uid uint32, logf func(string)) []string {
	var changed []string
	homeFD, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		logf(fmt.Sprintf("synopkgland: permissions not checked: HOME %s: %v", home, err))
		return nil
	}
	defer unix.Close(homeFD)
	// One line per path, even when several table entries share a parent.
	noted := map[string]bool{}
	note := func(p, format string, args ...any) {
		if !noted[p] {
			noted[p] = true
			logf("synopkgland: " + fmt.Sprintf(format, append([]any{p}, args...)...))
		}
	}
	for _, entry := range SensitivePaths {
		dir, name := path.Split(entry.Path)
		parent, err := openBelow(homeFD, strings.TrimSuffix(dir, "/"))
		if err != nil {
			if !errors.Is(err, unix.ENOENT) {
				note(path.Join(home, dir), "left %s unchanged: %v (symlinks are not followed)", err)
			}
			continue
		}
		names := []string{name}
		if entry.Glob {
			names = matchNames(parent, name, entry.Exclude)
		}
		for _, n := range names {
			full := path.Join(home, dir, n)
			if tightenAt(parent, n, entry.Dir, uid, full, note) {
				changed = append(changed, full)
			}
		}
		if parent != homeFD {
			unix.Close(parent)
		}
	}
	return changed
}

func openBelow(fd int, rel string) (int, error) {
	if rel == "" {
		return fd, nil
	}
	cur := fd
	for _, part := range strings.Split(rel, "/") {
		next, err := unix.Openat(cur, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if cur != fd {
			unix.Close(cur)
		}
		if err != nil {
			return -1, err
		}
		cur = next
	}
	return cur, nil
}

func matchNames(dirFD int, pattern, exclude string) []string {
	dup, err := unix.Dup(dirFD)
	if err != nil {
		return nil
	}
	f := os.NewFile(uintptr(dup), "dir")
	defer f.Close()
	entries, err := f.ReadDir(4096)
	if err != nil && len(entries) == 0 {
		return nil
	}
	var out []string
	for _, e := range entries {
		if ok, _ := path.Match(pattern, e.Name()); ok && (exclude == "" || !strings.HasSuffix(e.Name(), exclude)) {
			out = append(out, e.Name())
		}
	}
	return out
}

func tightenAt(parent int, name string, dir bool, uid uint32, full string, note func(string, string, ...any)) bool {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_NOCTTY | unix.O_CLOEXEC
	if dir {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(parent, name, flags, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return false
		}
		note(full, "left %s unchanged: %v (symlinks are not followed)", err)
		return false
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return false
	}
	kind := st.Mode & unix.S_IFMT
	if dir && kind != unix.S_IFDIR || !dir && kind != unix.S_IFREG {
		note(full, "left %s unchanged: unexpected file type")
		return false
	}
	if st.Uid != uid {
		note(full, "left %s unchanged: owned by UID %d, not the package UID %d", st.Uid, uid)
		return false
	}
	if st.Mode&0077 == 0 {
		return false
	}
	if err := unix.Fchmod(fd, st.Mode&07700); err != nil {
		note(full, "could not tighten %s: %v", err)
		return false
	}
	if unix.Fstat(fd, &st) == nil && st.Mode&0077 != 0 {
		note(full, "%s still allows group/other access after chmod (folder ACL); tools such as ssh may refuse it")
	}
	return true
}
