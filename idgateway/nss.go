package idgateway

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ErrNoAccount means NSS has no such DSM account, for example after DSM
// deleted it. DSM 7.4's authenticate.cgi still accepts a deleted account's
// session, so every verified name must also resolve here.
var ErrNoAccount = errors.New("no such DSM account")

// MinUID is the lowest account the gateway accepts; DSM users start at 1024.
const MinUID = 1000

// Directory maps DSM account names to uids through NSS: the local passwd
// file first, then id(1) for directory (AD/LDAP) accounts.
type Directory struct {
	Passwd string
	lookup func(ctx context.Context, args ...string) (string, error)
}

// NewDirectory reads /etc/passwd and asks /usr/bin/id for other accounts.
func NewDirectory() *Directory { return &Directory{Passwd: "/etc/passwd", lookup: runID} }

// UID returns the uid of the account name.
func (d *Directory) UID(ctx context.Context, name string) (int, error) {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, ":\n\x00") {
		return 0, ErrNoAccount
	}
	uid, found, err := d.scan(func(n string, u int) bool { return n == name })
	if err != nil {
		return 0, err
	}
	if !found {
		uid, err = d.ask(ctx, "-u", name)
		if err != nil {
			return 0, err
		}
	}
	return checkedUID(uid)
}

// Name returns the current name of the account uid.
func (d *Directory) Name(ctx context.Context, uid int) (string, error) {
	if _, err := checkedUID(uid); err != nil {
		return "", err
	}
	var name string
	_, found, err := d.scan(func(n string, u int) bool { name = n; return u == uid })
	if err != nil {
		return "", err
	}
	if found {
		return name, nil
	}
	if d.lookup == nil {
		return "", ErrNoAccount
	}
	out, err := d.lookup(ctx, "-nu", strconv.Itoa(uid))
	if err != nil || out == "" {
		return "", ErrNoAccount
	}
	return out, nil
}

func checkedUID(uid int) (int, error) {
	if uid < MinUID {
		return 0, ErrNoAccount
	}
	return uid, nil
}

func (d *Directory) ask(ctx context.Context, flag, name string) (int, error) {
	if d.lookup == nil {
		return 0, ErrNoAccount
	}
	out, err := d.lookup(ctx, flag, name)
	if err != nil {
		return 0, ErrNoAccount
	}
	uid, err := strconv.Atoi(out)
	if err != nil {
		return 0, ErrNoAccount
	}
	return uid, nil
}

// scan reads the passwd file on every call: account changes take effect at
// once and the file is small.
func (d *Directory) scan(match func(name string, uid int) bool) (int, bool, error) {
	entries, err := readPasswd(d.Passwd)
	if err != nil {
		return 0, false, err
	}
	for _, e := range entries {
		if match(e.name, e.uid) {
			return e.uid, true, nil
		}
	}
	return 0, false, nil
}

type passwdEntry struct {
	name string
	uid  int
}

// readPasswd parses a passwd file, skipping malformed lines.
func readPasswd(path string) ([]passwdEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []passwdEntry
	lines := bufio.NewScanner(bytes.NewReader(raw))
	for lines.Scan() {
		f := strings.Split(lines.Text(), ":")
		if len(f) < 7 {
			continue
		}
		if uid, err := strconv.Atoi(f[2]); err == nil {
			entries = append(entries, passwdEntry{name: f[0], uid: uid})
		}
	}
	return entries, nil
}

func runID(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/id", append(args[:1:1], "--", args[1])...)
	cmd.Env = []string{"LANG=C", "LC_ALL=C"}
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
