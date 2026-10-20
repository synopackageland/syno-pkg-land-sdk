//go:build linux

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMoveRejectsSymlinkHardlinkFIFOAndDestinationParent(t *testing.T) {
	base := t.TempDir()
	stage := filepath.Join(base, "stage")
	dest := filepath.Join(base, "dest")
	os.Mkdir(stage, 0700)
	os.Mkdir(dest, 0700)
	os.WriteFile(filepath.Join(stage, "source"), []byte("a"), 0600)
	di, err := bindDirectory(dest)
	if err != nil {
		t.Fatal(err)
	}
	w := subprocessWriter{Staging: stage, Executable: "/must-not-run", PackageUID: os.Getuid()}
	op := Operation{Job: "job", Name: "out", Staging: "source", Size: 1}
	d := Delegation{Root: dest, Directory: di}
	guard := func(publish func() (MoveReceipt, error)) (MoveReceipt, error) {
		t.Fatal("attack reached publish")
		return MoveReceipt{}, nil
	}
	for _, kind := range []string{"symlink", "hardlink", "fifo", "dest-parent"} {
		t.Run(kind, func(t *testing.T) {
			p := filepath.Join(stage, kind)
			switch kind {
			case "symlink":
				os.Symlink(filepath.Join(stage, "source"), p)
			case "hardlink":
				os.Link(filepath.Join(stage, "source"), p)
				defer os.Remove(p)
			case "fifo":
				syscall.Mkfifo(p, 0600)
			case "dest-parent":
				os.Symlink(dest, filepath.Join(base, "alias"))
				d.Root = filepath.Join(base, "alias")
				p = filepath.Join(stage, "source")
			}
			op.Staging = filepath.Base(p)
			if _, err := w.Move(context.Background(), Subject{UID: 1200, GID: 100, Groups: []int{100}}, op, d, guard); err == nil {
				t.Fatal("attack accepted")
			}
		})
	}
}
func TestMoveChild(t *testing.T) {
	mode := os.Getenv("W3_MOVE_CHILD")
	if mode == "" {
		return
	}
	if delay, err := time.ParseDuration(os.Getenv("W3_SLOW_DIR_SYNC")); err == nil {
		sync := syncPublished
		syncPublished = func(dir *os.File) error { time.Sleep(delay); return sync(dir) }
	}
	var err error
	switch mode {
	case "move-child":
		err = runMoveChild()
	case "part-cleanup-child":
		err = runPartCleanupChild()
	case "source-cleanup-child":
		err = runSourceCleanupChild()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
func TestMoveRealUIDLargeFileReceiptAndCleanup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root Linux fixture required")
	}
	base := t.TempDir()
	os.Chmod(base, 0755)
	os.Chmod(filepath.Dir(base), 0755)
	stage := filepath.Join(base, "stage")
	dest := filepath.Join(base, "dest")
	os.Mkdir(stage, 0700)
	os.Mkdir(dest, 0700)
	pkg := Subject{UID: 1400, GID: 1400, Groups: []int{1400}}
	s := Subject{Username: "admin-fixture", UID: 1200, GID: 100, Groups: []int{100}, Admin: true}
	os.Chown(stage, pkg.UID, pkg.GID)
	os.Chown(dest, s.UID, s.GID)
	source := filepath.Join(stage, "large")
	f, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(1 << 30)
	f.Truncate(size)
	f.WriteAt([]byte("end"), size-3)
	f.Close()
	// The producer's private inode is unreadable by the destination subject.
	// The privileged parent must hand off its already-open read-only FD.
	if err := os.Chmod(source, 0600); err != nil {
		t.Fatal(err)
	}
	os.Chown(source, pkg.UID, pkg.GID)
	input, _ := os.Open(source)
	wantHash := sha256.New()
	io.Copy(wantHash, input)
	input.Close()
	exe, _ := os.Executable()
	launcher := filepath.Join(base, "child")
	os.WriteFile(launcher, []byte("#!/bin/sh\nexport W3_MOVE_CHILD=\"$1\"\nexec "+exe+" -test.run=^TestMoveChild$\n"), 0700)
	di, _ := bindDirectory(dest)
	d := Delegation{Root: dest, Directory: di}
	op := Operation{Job: "job", Name: "out", Staging: "large", Size: size}
	w := subprocessWriter{Staging: stage, Executable: launcher, PackageUID: pkg.UID, PackageGID: pkg.GID}
	receipt, err := w.Move(context.Background(), s, op, d, func(publish func() (MoveReceipt, error)) (MoveReceipt, error) { return publish() })
	if err != nil || receipt.CleanupPending {
		t.Fatal(receipt, err)
	}
	info, err := os.Stat(filepath.Join(dest, "out"))
	if err != nil || info.Size() != size || info.Sys().(*syscall.Stat_t).Uid != uint32(s.UID) {
		t.Fatal(info, err)
	}
	output, _ := os.Open(filepath.Join(dest, "out"))
	gotHash := sha256.New()
	io.Copy(gotHash, output)
	output.Close()
	if string(wantHash.Sum(nil)) != string(gotHash.Sum(nil)) {
		t.Fatal("large file checksum")
	}
	if _, err = os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("source cleanup", err)
	}
	// Existing destination must remain complete; cleanup failure does not turn it
	// into a retryable copy or remove the package source.
	os.WriteFile(source, []byte("x"), 0600)
	os.Chown(source, pkg.UID, pkg.GID)
	op.Size = 1
	if _, err = w.Move(context.Background(), s, op, d, func(publish func() (MoveReceipt, error)) (MoveReceipt, error) { return publish() }); err == nil {
		t.Fatal("overwrote existing final")
	}
	info, _ = os.Stat(filepath.Join(dest, "out"))
	if info.Size() != size {
		t.Fatal("changed final")
	}
}
func TestMoveBoundedActualBytes(t *testing.T) {
	// A reader can grow after stat. The child checks size+1, rather than copying
	// arbitrary excess or silently truncating to the declaration.
	src, err := os.CreateTemp(t.TempDir(), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	src.WriteString("abcd")
	src.Seek(0, 0)
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err = copyExact(out, src, 3); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatal(err)
	}
}

func TestMoveOpenPartResistsReplacementAndDestinationSwap(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root Linux fixture required")
	}
	for _, attack := range []string{"part", "directory"} {
		t.Run(attack, func(t *testing.T) {
			base := t.TempDir()
			os.Chmod(base, 0755)
			os.Chmod(filepath.Dir(base), 0755)
			stage := filepath.Join(base, "stage")
			dest := filepath.Join(base, "dest")
			os.Mkdir(stage, 0700)
			os.Mkdir(dest, 0700)
			os.Chown(stage, 1400, 1400)
			os.Chown(dest, 1200, 100)
			source := filepath.Join(stage, "source")
			os.WriteFile(source, []byte("original"), 0600)
			os.Chown(source, 1400, 1400)
			exe, _ := os.Executable()
			launcher := filepath.Join(base, "child")
			os.WriteFile(launcher, []byte("#!/bin/sh\nexport W3_MOVE_CHILD=\"$1\"\nexec "+exe+" -test.run=^TestMoveChild$\n"), 0700)
			di, _ := bindDirectory(dest)
			w := subprocessWriter{Staging: stage, Executable: launcher, PackageUID: 1400, PackageGID: 1400}
			s := Subject{UID: 1200, GID: 100, Groups: []int{100}}
			receipt, err := w.Move(context.Background(), s, Operation{Job: "job", Staging: "source", Name: "out", Size: 8}, Delegation{Root: dest, Directory: di}, func(publish func() (MoveReceipt, error)) (MoveReceipt, error) {
				if attack == "part" {
					entries, _ := os.ReadDir(dest)
					for _, ent := range entries {
						if strings.HasSuffix(ent.Name(), ".part") {
							p := filepath.Join(dest, ent.Name())
							os.Remove(p)
							os.WriteFile(p, []byte("replacement"), 0600)
							os.Chown(p, s.UID, s.GID)
						}
					}
				} else {
					os.Rename(dest, dest+"-moved")
					os.Mkdir(dest, 0700)
					os.Chown(dest, s.UID, s.GID)
				}
				return publish()
			})
			if attack == "part" {
				if err == nil {
					t.Fatal("unlinked part published")
				}
				if _, x := os.Stat(filepath.Join(dest, "out")); !os.IsNotExist(x) {
					t.Fatal("replacement became final")
				}
				entries, _ := os.ReadDir(dest)
				if len(entries) != 1 {
					t.Fatal("replacement part removed", entries)
				}
				_ = receipt

			} else {
				if err == nil {
					t.Fatal("changed destination published")
				}
				for _, path := range []string{dest, dest + "-moved"} {
					if _, x := os.Stat(filepath.Join(path, "out")); !os.IsNotExist(x) {
						t.Fatal("swap wrote final", path)
					}
				}
			}
		})
	}
}

func TestPublishProtocolChild(t *testing.T) {
	if os.Getenv("W3_PUBLISH_FAULT") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	reader.ReadBytes('\n')
	json.NewEncoder(os.Stdout).Encode(childEvent{Kind: "part", Part: directoryIdentity{Inode: uint64(os.Getpid())}})
	reader.ReadString('\n')
	json.NewEncoder(os.Stdout).Encode(childEvent{Kind: "ready"})
	reader.ReadString('\n')
	json.NewEncoder(os.Stdout).Encode(childEvent{Kind: "malformed"})
	time.Sleep(time.Minute)
	os.Exit(1)
}
func TestPublishLostReplyDrainsChildBeforeCommitLockRelease(t *testing.T) {
	base := t.TempDir()
	stage := filepath.Join(base, "stage")
	dest := filepath.Join(base, "dest")
	os.Mkdir(stage, 0700)
	os.Mkdir(dest, 0700)
	os.WriteFile(filepath.Join(stage, "source"), []byte("x"), 0600)
	exe, _ := os.Executable()
	launcher := filepath.Join(base, "child")
	os.WriteFile(launcher, []byte("#!/bin/sh\nexport W3_PUBLISH_FAULT=1\nexec "+exe+" -test.run=^TestPublishProtocolChild$\n"), 0700)
	di, _ := bindDirectory(dest)
	w := subprocessWriter{Staging: stage, Executable: launcher, PackageUID: os.Getuid()}
	pid := 0
	ctx := context.WithValue(context.Background(), partRecordKey{}, func(part partMetadata) error { pid = int(part.Part.Inode); return nil })
	_, err := w.Move(ctx, Subject{UID: 1200, GID: 100, Groups: []int{100}}, Operation{Job: "job", Staging: "source", Name: "out", Size: 1}, Delegation{Root: dest, Directory: di}, func(publish func() (MoveReceipt, error)) (MoveReceipt, error) {
		receipt, err := publish()
		if err == nil {
			t.Fatal("malformed reply accepted")
		}
		if pid == 0 || syscall.Kill(pid, 0) != syscall.ESRCH {
			t.Fatal("child survived commit lock release", pid)
		}
		return receipt, err
	})
	if err == nil {
		t.Fatal("lost reply succeeded")
	}
}

// B3: a slow directory fsync after link (large file, loaded volume) outlives the
// receipt bound. The child is drained; the exact part inode already under the
// final name is a commit, not an uncertain result, and is never written twice.
func TestMoveLostReceiptAfterLinkConvergesToCommitted(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root Linux fixture required")
	}
	defer func(old time.Duration) { publishReceiptTimeout = old }(publishReceiptTimeout)
	publishReceiptTimeout = time.Second
	base := t.TempDir()
	os.Chmod(base, 0755)
	os.Chmod(filepath.Dir(base), 0755)
	stage := filepath.Join(base, "stage")
	dest := filepath.Join(base, "dest")
	os.Mkdir(stage, 0700)
	os.Mkdir(dest, 0700)
	pkg := Subject{UID: 1400, GID: 1400, Groups: []int{1400}}
	s := Subject{Username: "normal-fixture", UID: 1216, GID: 100, Groups: []int{100}}
	os.Chown(stage, pkg.UID, pkg.GID)
	os.Chown(dest, s.UID, s.GID)
	source := filepath.Join(stage, "big")
	size := int64(1 << 30)
	f, err := os.OpenFile(source, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.New()
	block := make([]byte, 1<<20)
	for i := int64(0); i < size/int64(len(block)); i++ {
		block[0], block[len(block)-1] = byte(i), byte(i>>8)
		f.Write(block)
		wantHash.Write(block)
	}
	f.Close()
	os.Chown(source, pkg.UID, pkg.GID)
	exe, _ := os.Executable()
	launcher := filepath.Join(base, "child")
	os.WriteFile(launcher, []byte("#!/bin/sh\nexport W3_MOVE_CHILD=\"$1\" W3_SLOW_DIR_SYNC=4s\nexec "+exe+" -test.run=^TestMoveChild$\n"), 0700)
	di, _ := bindDirectory(dest)
	w := subprocessWriter{Staging: stage, Executable: launcher, PackageUID: pkg.UID, PackageGID: pkg.GID}
	var part directoryIdentity
	ctx := context.WithValue(context.Background(), partRecordKey{}, func(p partMetadata) error { part = p.Part; return nil })
	publishes := 0
	receipt, err := w.Move(ctx, s, Operation{Job: "job", Name: "out.bin", Staging: "big", Size: size}, Delegation{Root: dest, Directory: di}, func(publish func() (MoveReceipt, error)) (MoveReceipt, error) {
		publishes++
		return publish()
	})
	if err != nil || publishes != 1 {
		t.Fatal("lost receipt after link stayed uncertain", receipt, err, publishes)
	}
	final, err := os.Open(filepath.Join(dest, "out.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer final.Close()
	got, _ := fileIdentity(final)
	info, _ := final.Stat()
	if got != part || info.Size() != size || info.Sys().(*syscall.Stat_t).Uid != uint32(s.UID) || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		t.Fatal("final is not the copied part", got, part, info.Size())
	}
	gotHash := sha256.New()
	io.Copy(gotHash, final)
	if string(gotHash.Sum(nil)) != string(wantHash.Sum(nil)) {
		t.Fatal("final checksum")
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 1 {
		t.Fatal("part left behind", entries)
	}
	if _, err = os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("source cleanup", err)
	}
}

func TestPublishedPartMatchesOnlyTheRecordedInode(t *testing.T) {
	dest := t.TempDir()
	os.WriteFile(filepath.Join(dest, "part"), []byte("abc"), 0600)
	os.WriteFile(filepath.Join(dest, "other"), []byte("abc"), 0600)
	os.Link(filepath.Join(dest, "part"), filepath.Join(dest, "out"))
	dir, _ := os.Open(dest)
	defer dir.Close()
	di, _ := fileIdentity(dir)
	part, _ := os.Open(filepath.Join(dest, "part"))
	pi, _ := fileIdentity(part)
	part.Close()
	op := Operation{Name: "out", Size: 3, Subject: Subject{UID: os.Getuid()}, Part: &partMetadata{Directory: di, Part: pi, Destination: dest}}
	w := subprocessWriter{}
	if _, ok := w.PublishedPart(context.Background(), op); !ok {
		t.Fatal("recorded part under final name not recognized")
	}
	for name, mutate := range map[string]func(*Operation){
		"other inode": func(o *Operation) { o.Name = "other" },
		"absent":      func(o *Operation) { o.Name = "missing" },
		"size":        func(o *Operation) { o.Size = 4 },
		"owner":       func(o *Operation) { o.Subject.UID++ },
		"directory":   func(o *Operation) { p := *o.Part; p.Directory.Inode++; o.Part = &p },
	} {
		o := op
		mutate(&o)
		if _, ok := w.PublishedPart(context.Background(), o); ok {
			t.Fatal("accepted", name)
		}
	}
}

// When serve dies its end of the child's control pipe closes. The child must
// stop copying and remove its part instead of filling it until the copy ends.
func TestMoveChildStopsAndRemovesPartWhenParentDies(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root Linux fixture required")
	}
	base := t.TempDir()
	os.Chmod(base, 0755)
	os.Chmod(filepath.Dir(base), 0755)
	dest := filepath.Join(base, "dest")
	os.Mkdir(dest, 0700)
	s := Subject{UID: 1200, GID: 100, Groups: []int{100}}
	os.Chown(dest, s.UID, s.GID)
	size := int64(2 << 30)
	source, err := os.Create(filepath.Join(base, "large"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	source.Truncate(size)
	di, _ := bindDirectory(dest)
	dir, err := openDirectory(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	exe, _ := os.Executable()
	launcher := filepath.Join(base, "child")
	os.WriteFile(launcher, []byte("#!/bin/sh\nexport W3_MOVE_CHILD=\"$1\"\nexec "+exe+" -test.run=^TestMoveChild$\n"), 0700)
	cmd := command(context.Background(), launcher, []string{"move-child"})
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.ExtraFiles = []*os.File{source, dir}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Cancel()
	spec := moveSpec{SourceUID: 0, Subject: s, Destination: dest, Directory: di, Name: "out", Size: size, Part: partMetadata{Temporary: ".synopkgland-job-orphan.part", Directory: di, Destination: dest}}
	json.NewEncoder(input).Encode(spec)
	var ev childEvent
	line, err := bufio.NewReader(output).ReadBytes('\n')
	if err != nil || json.Unmarshal(line, &ev) != nil || ev.Kind != "part" {
		t.Fatal("child part", string(line), err)
	}
	input.Write([]byte("C\n"))
	time.Sleep(200 * time.Millisecond)
	input.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err = <-exited:
		if err == nil {
			t.Fatal("orphaned child reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child kept copying after its parent's pipe closed")
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 0 {
		t.Fatal("orphaned child left files", entries)
	}
}

func TestPartIsRemovedAfterTheSubjectLosesWriteAccess(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root Linux fixture required")
	}
	base := t.TempDir()
	os.Chmod(base, 0755)
	os.Chmod(filepath.Dir(base), 0755)
	dest := filepath.Join(base, "dest")
	os.Mkdir(dest, 0700)
	s := Subject{Username: "fixture", UID: 1200, GID: 100, Groups: []int{100}}
	os.Chown(dest, s.UID, s.GID)
	name := ".synopkgland-job-00.part"
	os.WriteFile(filepath.Join(dest, name), []byte("partial"), 0600)
	os.Chown(filepath.Join(dest, name), s.UID, s.GID)
	part, _ := os.Open(filepath.Join(dest, name))
	identity, err := fileIdentity(part)
	part.Close()
	di, _ := bindDirectory(dest)
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(dest, 0500)
	exe, _ := os.Executable()
	launcher := filepath.Join(base, "child")
	os.WriteFile(launcher, []byte("#!/bin/sh\nexport W3_MOVE_CHILD=\"$1\"\nexec "+exe+" -test.run=^TestMoveChild$\n"), 0755)
	w := subprocessWriter{Executable: launcher, PackageUID: 1400, PackageGID: 1400}
	if err := w.CleanupRecorded(context.Background(), s, partMetadata{Temporary: name, Directory: di, Part: identity, Destination: dest}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dest, name)); !os.IsNotExist(err) {
		t.Fatal("part left behind", err)
	}
}
