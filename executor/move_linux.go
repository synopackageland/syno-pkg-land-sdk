//go:build linux

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// publishReceiptTimeout bounds the only I/O done under the commit lock.
var publishReceiptTimeout = 5 * time.Second

// syncPublished makes the child's link durable; tests slow it down.
var syncPublished = func(dir *os.File) error { return dir.Sync() }

type moveSpec struct {
	SourceUID   int               `json:"source_uid"`
	Subject     Subject           `json:"subject"`
	Destination string            `json:"destination"`
	Directory   directoryIdentity `json:"directory"`
	Name        string            `json:"name"`
	Conflict    string            `json:"conflict,omitempty"`
	Size        int64             `json:"size"`
	Part        partMetadata      `json:"part"`
	SourceName  string            `json:"source_name,omitempty"`
}
type childEvent struct {
	Code    string            `json:"code,omitempty"`
	Kind    string            `json:"kind"`
	Part    directoryIdentity `json:"part,omitempty"`
	Receipt MoveReceipt       `json:"receipt,omitempty"`
}

func fileIdentity(f *os.File) (directoryIdentity, error) {
	info, err := f.Stat()
	if err != nil {
		return directoryIdentity{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return directoryIdentity{}, errors.New("stat")
	}
	return directoryIdentity{uint64(st.Dev), st.Ino}, nil
}
func openDirectory(path string) (*os.File, error) {
	if !cleanPath(path) || path == "/" {
		return nil, errors.New("directory path")
	}
	return openBeneath("/", strings.TrimPrefix(path, "/"), syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
}
func checkDirectory(path string, pinned *os.File, expected directoryIdentity) (*os.File, error) {
	dir, err := openDirectory(path)
	if err != nil {
		return nil, err
	}
	got, err := fileIdentity(dir)
	pin, x := fileIdentity(pinned)
	if err != nil || x != nil || got != expected || got != pin {
		dir.Close()
		return nil, errors.New("directory replaced")
	}
	return dir, nil
}
func (w subprocessWriter) openSource(relative string) (*os.File, string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(w.Staging))
	if err != nil {
		return nil, "", err
	}
	root := filepath.Join(parent, filepath.Base(w.Staging))
	f, err := openBeneath(root, relative, syscall.O_RDONLY, 0)
	if err != nil {
		return nil, "", err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, "", errors.New("staging file")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != w.PackageUID || st.Nlink != 1 {
		f.Close()
		return nil, "", errors.New("staging owner or hardlink")
	}
	return f, root, nil
}
func (w subprocessWriter) Move(ctx context.Context, s Subject, op Operation, d Delegation, guard func(func() (MoveReceipt, error)) (MoveReceipt, error)) (MoveReceipt, error) {
	if !tokenRE.MatchString(op.Job) || !validDestinationName(op.Name) || (op.Conflict != "" && !validConflict(op.Conflict)) || op.Size < 0 || op.Size > maxMoveBytes {
		return MoveReceipt{}, errors.New("request")
	}
	f, root, err := w.openSource(op.Staging)
	if err != nil {
		return MoveReceipt{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() != op.Size {
		return MoveReceipt{}, errors.New("size mismatch")
	}
	dir, err := openDirectory(d.Root)
	if err != nil {
		return MoveReceipt{}, err
	}
	defer dir.Close()
	di, err := fileIdentity(dir)
	if err != nil || di != d.Directory {
		return MoveReceipt{}, errors.New("destination replaced")
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return MoveReceipt{}, err
	}
	spec := moveSpec{SourceUID: w.PackageUID, Subject: s, Destination: d.Root, Directory: d.Directory, Name: op.Name, Conflict: op.Conflict, Size: op.Size, Part: partMetadata{Temporary: ".synopkgland-" + op.Job + "-" + hex.EncodeToString(random) + ".part", Directory: d.Directory, Destination: d.Root}}
	// Separate pipes allow a fixed child to copy first and wait for the parent's
	// commit decision. There is no socket endpoint or arbitrary child payload.
	input, inWrite, err := os.Pipe()
	if err != nil {
		return MoveReceipt{}, err
	}
	defer input.Close()
	defer inWrite.Close()
	output, outWrite, err := os.Pipe()
	if err != nil {
		return MoveReceipt{}, err
	}
	defer output.Close()
	defer outWrite.Close()
	cmd := command(ctx, w.Executable, []string{"move-child"})
	cmd.Stdin = input
	cmd.Stdout = outWrite
	cmd.Stderr = io.Discard
	cmd.ExtraFiles = []*os.File{f, dir}
	if err = cmd.Start(); err != nil {
		return MoveReceipt{}, err
	}
	input.Close()
	outWrite.Close()
	waited := false
	defer func() {
		if !waited {
			cmd.Cancel()
			cmd.Wait()
		}
	}()
	defer func() {
		if spec.Part.Part.Inode != 0 {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			w.cleanupPart(cleanup, spec, dir)
		}
	}()
	if err = json.NewEncoder(inWrite).Encode(spec); err != nil {
		return MoveReceipt{}, err
	}
	reader := bufio.NewReader(output)
	event := func() (childEvent, error) {
		var event childEvent
		b, err := reader.ReadBytes('\n')
		if err != nil || len(b) > maxRequest {
			return event, errors.New("child receipt")
		}
		if strictJSON(b, &event) != nil {
			return event, errors.New("child receipt")
		}
		return event, nil
	}
	ev, err := event()
	if err != nil || ev.Kind != "part" || ev.Part.Inode == 0 {
		return MoveReceipt{}, errors.New("child part")
	}
	spec.Part.Part = ev.Part
	if record, ok := ctx.Value(partRecordKey{}).(func(partMetadata) error); ok {
		if err = record(spec.Part); err != nil {
			return MoveReceipt{}, err
		}
	}
	if _, err = inWrite.Write([]byte("C\n")); err != nil {
		return MoveReceipt{}, err
	}
	ev, err = event()
	if err != nil || ev.Kind != "ready" {
		return MoveReceipt{}, errors.New("child copy")
	}
	receipt, err := guard(func() (MoveReceipt, error) {
		// This is the only bounded I/O under the commit lock. Lost ack is uncertain.
		if x := output.SetReadDeadline(time.Now().Add(publishReceiptTimeout)); x != nil {
			return MoveReceipt{}, x
		}
		if _, x := inWrite.Write([]byte("P\n")); x != nil {
			cmd.Cancel()
			cmd.Wait()
			waited = true
			return MoveReceipt{}, x
		}
		ev, x := event()
		if x != nil || ev.Kind != "committed" {
			// Drain before releasing the protected publish lock: a lost receipt must
			// never leave a live child that could publish after revoke acknowledges.
			cmd.Cancel()
			cmd.Wait()
			waited = true
		}
		if x == nil && ev.Kind == "refused" && contains([]string{"destination-exists", "PERMISSION_DENIED", "NO_SPACE", "destination-not-directory", "name-too-long"}, ev.Code) {
			return MoveReceipt{}, publishRejectedError{ev.Code}
		}
		if x != nil || ev.Kind != "committed" {
			// The child may have linked before its receipt was lost, e.g. while a
			// large file's directory fsync outlived the bound. With the child gone
			// the final name is settled: our exact part inode there is the commit.
			if found, ok := publishedPart(dir, spec); waited && ok && dir.Sync() == nil {
				return MoveReceipt{Name: found}, nil
			}
			return MoveReceipt{}, errors.New("publish receipt")
		}
		return ev.Receipt, nil
	})
	if err != nil {
		return receipt, err
	}
	if !waited && cmd.Wait() != nil {
		receipt.CleanupPending = true
	}
	waited = true
	// Committed is immutable even if part/source cleanup fails.
	if err = w.cleanupPart(ctx, spec, dir); err != nil {
		receipt.CleanupPending = true
	} else {
		spec.Part.Part = directoryIdentity{}
	}
	if err = w.cleanupSource(ctx, f, root, op.Staging); err != nil {
		receipt.CleanupPending = true
	}
	return receipt, nil
}

// publishedPart reports whether the final name is the recorded part inode: a
// regular file of the declared size owned by the subject. It never creates.
func publishedPart(dir *os.File, spec moveSpec) (string, bool) {
	folders, name := destinationFolders(spec.Name)
	leaf, err := openFolders(dir, folders, false)
	if err != nil {
		return "", false
	}
	defer leaf.Close()
	candidates := []string{name}
	if spec.Conflict == conflictRename {
		for n := 1; n <= maxRenameAttempts; n++ {
			candidates = append(candidates, renameCandidate(name, n))
		}
	}
	for _, candidate := range candidates {
		if partAt(leaf, candidate, spec) {
			return strings.Join(append(folders, candidate), "/"), true
		}
	}
	return "", false
}
func partAt(leaf *os.File, name string, spec moveSpec) bool {
	fd, err := syscall.Openat(int(leaf.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	got, err := fileIdentity(f)
	info, x := f.Stat()
	if err != nil || x != nil || !info.Mode().IsRegular() || info.Size() != spec.Size || spec.Part.Part.Inode == 0 || got != spec.Part.Part {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == spec.Subject.UID
}

// cleanupPart removes the part as the subject. A subject who lost write access
// cannot, so root then removes only that exact inode from the pinned directory.
func (w subprocessWriter) cleanupPart(ctx context.Context, spec moveSpec, dir *os.File) error {
	b, _ := json.Marshal(spec)
	cmd := command(ctx, w.Executable, []string{"part-cleanup-child"})
	cmd.ExtraFiles = []*os.File{dir}
	cmd.Stdin = strings.NewReader(string(b))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if cmd.Run() == nil {
		return nil
	}
	got, err := fileIdentity(dir)
	if err != nil || got != spec.Directory || !validTemporary(spec.Part.Temporary) || spec.Part.Part.Inode == 0 {
		return errors.New("part cleanup")
	}
	if err = unlinkExact(dir, spec.Part.Temporary, spec.Part.Part); err != nil && !errors.Is(err, syscall.ENOENT) {
		return err
	}
	return dir.Sync()
}
func (w subprocessWriter) cleanupSource(ctx context.Context, original *os.File, root, relative string) error {
	parent := filepath.Dir(relative)
	path := root
	if parent != "." {
		path = filepath.Join(root, parent)
	}
	dir, err := openDirectory(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	spec := moveSpec{Subject: Subject{UID: w.PackageUID, GID: w.PackageGID, Groups: []int{w.PackageGID}}, SourceName: filepath.Base(relative)}
	b, _ := json.Marshal(spec)
	cmd := command(ctx, w.Executable, []string{"source-cleanup-child"})
	cmd.ExtraFiles = []*os.File{original, dir}
	cmd.Stdin = strings.NewReader(string(b))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}
func readMoveSpec(r io.Reader) (moveSpec, error) {
	var s moveSpec
	b, err := io.ReadAll(io.LimitReader(r, maxRequest+1))
	if err != nil || len(b) > maxRequest || strictJSON(b, &s) != nil {
		return s, errors.New("spec")
	}
	return s, nil
}
func runMoveChild() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if os.Geteuid() != 0 {
		return errors.New("root child required")
	}
	input := bufio.NewReader(os.Stdin)
	line, err := input.ReadBytes('\n')
	if err != nil || len(line) > maxRequest {
		return errors.New("spec")
	}
	var spec moveSpec
	if strictJSON(line, &spec) != nil || !validDestinationName(spec.Name) || (spec.Conflict != "" && !validConflict(spec.Conflict)) || !validTemporary(spec.Part.Temporary) || spec.Size < 0 || spec.Size > maxMoveBytes {
		return errors.New("spec")
	}
	if err = dropSealedIdentity(realIdentity{}, spec.Subject); err != nil {
		return err
	}
	pinned := os.NewFile(4, "destination")
	defer pinned.Close()
	dir, err := checkDirectory(spec.Destination, pinned, spec.Directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	fd, err := syscall.Openat(int(dir.Fd()), spec.Part.Temporary, syscall.O_RDWR|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	part := os.NewFile(uintptr(fd), spec.Part.Temporary)
	defer part.Close()
	identity, err := fileIdentity(part)
	if err != nil {
		return err
	}
	defer unlinkExact(dir, spec.Part.Temporary, identity)
	send := func(event childEvent) error { return json.NewEncoder(os.Stdout).Encode(event) }
	if err = send(childEvent{Kind: "part", Part: identity}); err != nil {
		return err
	}
	decision, err := input.ReadString('\n')
	if err != nil || decision != "C\n" {
		return errors.New("copy refused")
	}
	src := os.NewFile(3, "staging")
	defer src.Close()
	// The parent's death closes stdin; stop copying instead of orphaning.
	control := awaitControl(input)
	if err = copyUnlessParentGone(part, src, spec.Size, control); err != nil {
		return err
	}
	info, err := src.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("staging changed")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != spec.SourceUID || st.Nlink != 1 || info.Size() != spec.Size {
		return errors.New("staging changed")
	}
	if err = part.Sync(); err != nil {
		return err
	}
	if err = send(childEvent{Kind: "ready"}); err != nil {
		return err
	}
	if next := <-control; next.err != nil || next.line != "P\n" {
		return errors.New("publish refused")
	}
	check, err := checkDirectory(spec.Destination, pinned, spec.Directory)
	if err != nil {
		return err
	}
	check.Close()
	// Folders are made only now, as the subject and after the final fence, so a
	// canceled or revoked move leaves none behind. The part stays in dir.
	folders, name := destinationFolders(spec.Name)
	leaf, err := openFolders(dir, folders, true)
	if err != nil {
		if code := publishCode(err); code != "" {
			return send(childEvent{Kind: "refused", Code: code})
		}
		return err
	}
	defer leaf.Close()
	published, err := publishPart(part, leaf, name, spec.Conflict)
	if err != nil {
		if code := publishCode(err); code != "" {
			return send(childEvent{Kind: "refused", Code: code})
		}
		return err
	}
	// A failure to sync after link is a lost durable result, not a retryable error.
	if err = syncPublished(leaf); err != nil {
		return err
	}
	cleanupPending := unlinkExact(dir, spec.Part.Temporary, identity) != nil
	if !cleanupPending {
		cleanupPending = dir.Sync() != nil
	}
	receipt := MoveReceipt{CleanupPending: cleanupPending}
	if published != name {
		receipt.Name = strings.Join(append(folders, published), "/")
	}
	return send(childEvent{Kind: "committed", Receipt: receipt})
}

// publishPart gives the OPEN part inode its final name in leaf, never a
// possibly swapped pathname, and returns the name used. The default policy
// refuses a taken name; rename tries "name (n).ext"; overwrite links a
// temporary and renames it over a regular file the user may write, so the old
// file is replaced whole or not at all.
func publishPart(part, leaf *os.File, name, conflict string) (string, error) {
	switch conflict {
	case conflictRename:
		err := linkOpenPart(part, leaf, name)
		for n := 1; err == syscall.EEXIST && n <= maxRenameAttempts; n++ {
			candidate := renameCandidate(name, n)
			if err = linkOpenPart(part, leaf, candidate); err == nil {
				return candidate, nil
			}
		}
		return name, err
	case conflictOverwrite:
		if err := replaceable(leaf, name); err != nil {
			return name, err
		}
		random := make([]byte, 8)
		if _, err := rand.Read(random); err != nil {
			return name, err
		}
		temporary := ".synopkgland-replace-" + hex.EncodeToString(random) + ".part"
		if err := linkOpenPart(part, leaf, temporary); err != nil {
			return name, err
		}
		if err := syscall.Renameat(int(leaf.Fd()), temporary, int(leaf.Fd()), name); err != nil {
			if identity, x := fileIdentity(part); x == nil {
				unlinkExact(leaf, temporary, identity)
			}
			return name, err
		}
		return name, nil
	}
	// AT_SYMLINK_FOLLOW is needed only for the trusted proc FD; the destination
	// name cannot be replaced.
	return name, linkOpenPart(part, leaf, name)
}

// replaceable allows overwrite of a missing name or of a regular file the user
// can open for writing, as File Station requires. Folders, symlinks and devices
// are refused as taken names.
func replaceable(leaf *os.File, name string) error {
	fd, err := syscall.Openat(int(leaf.Fd()), name, syscall.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	switch err {
	case nil:
	case syscall.ENOENT:
		return nil
	case syscall.EACCES, syscall.EPERM:
		return err
	default:
		return syscall.EEXIST
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return syscall.EEXIST
	}
	return nil
}

// publishCode names the publish refusals an App can act on; others are failures.
func publishCode(err error) string {
	switch err {
	case syscall.EEXIST:
		return "destination-exists"
	case syscall.EACCES, syscall.EPERM:
		return "PERMISSION_DENIED"
	case syscall.ENOSPC:
		return "NO_SPACE"
	case syscall.ENOTDIR, syscall.ELOOP, errCrossDevice:
		return "destination-not-directory"
	case syscall.ENAMETOOLONG:
		return "name-too-long"
	}
	return ""
}

var errCrossDevice = errors.New("folder on another device")

// openFolders opens the folders below dir one component at a time, never
// following a symlink or entering another mount, and returns the last one.
// With create, the caller's identity makes each missing folder, so its owner
// and inherited ACL match a folder the user makes in File Station; 0700 mirrors
// the 0600 file mode and leaves wider access to the share's ACL. A folder that
// exists, including one a previous attempt made, is reused.
func openFolders(dir *os.File, folders []string, create bool) (*os.File, error) {
	fd, err := syscall.Openat(int(dir.Fd()), ".", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	current := os.NewFile(uintptr(fd), ".")
	for _, folder := range folders {
		next, err := openFolder(current, folder, create)
		current.Close()
		if err != nil {
			return nil, err
		}
		current = next
	}
	return current, nil
}
func openFolder(parent *os.File, name string, create bool) (*os.File, error) {
	const flags = syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC | syscall.O_NONBLOCK
	fd, err := syscall.Openat(int(parent.Fd()), name, flags, 0)
	if err == syscall.ENOENT && create {
		if err = syscall.Mkdirat(int(parent.Fd()), name, 0700); err == nil {
			err = parent.Sync()
		} else if err == syscall.EEXIST {
			err = nil
		}
		if err == nil {
			fd, err = syscall.Openat(int(parent.Fd()), name, flags, 0)
		}
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	got, err := fileIdentity(f)
	above, x := fileIdentity(parent)
	if err != nil || x != nil {
		f.Close()
		return nil, errors.New("stat")
	}
	if got.Device != above.Device {
		f.Close()
		return nil, errCrossDevice
	}
	return f, nil
}
func unlinkExact(dir *os.File, name string, expected directoryIdentity) error {
	fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err == syscall.ENOENT {
		return nil
	}
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	got, err := fileIdentity(f)
	info, x := f.Stat()
	f.Close()
	if err != nil || x != nil || !info.Mode().IsRegular() || got != expected {
		return errors.New("cleanup replaced")
	}
	return syscall.Unlinkat(int(dir.Fd()), name)
}
func runPartCleanupChild() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if os.Geteuid() != 0 {
		return errors.New("root child required")
	}
	spec, err := readMoveSpec(os.Stdin)
	if err != nil || !validTemporary(spec.Part.Temporary) || spec.Part.Part.Inode == 0 {
		return errors.New("spec")
	}
	if err = dropSealedIdentity(realIdentity{}, spec.Subject); err != nil {
		return err
	}
	dir := os.NewFile(3, "directory")
	defer dir.Close()
	got, err := fileIdentity(dir)
	if err != nil || got != spec.Directory {
		return errors.New("directory")
	}
	if err = unlinkExact(dir, spec.Part.Temporary, spec.Part.Part); err != nil {
		return err
	}
	return dir.Sync()
}
func runSourceCleanupChild() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if os.Geteuid() != 0 {
		return errors.New("root child required")
	}
	spec, err := readMoveSpec(os.Stdin)
	if err != nil || !validName(spec.SourceName) {
		return errors.New("spec")
	}
	if err = dropSealedIdentity(realIdentity{}, spec.Subject); err != nil {
		return err
	}
	src := os.NewFile(3, "source")
	defer src.Close()
	dir := os.NewFile(4, "directory")
	defer dir.Close()
	identity, err := fileIdentity(src)
	if err != nil {
		return err
	}
	if err = unlinkExact(dir, spec.SourceName, identity); err != nil {
		return err
	}
	return dir.Sync()
}

func (w subprocessWriter) PublishedPart(_ context.Context, op Operation) (string, bool) {
	if op.Part == nil || !validDestinationName(op.Name) {
		return "", false
	}
	dir, err := openDirectory(op.Part.Destination)
	if err != nil {
		return "", false
	}
	defer dir.Close()
	got, err := fileIdentity(dir)
	if err != nil || got != op.Part.Directory {
		return "", false
	}
	name, ok := publishedPart(dir, moveSpec{Subject: op.Subject, Name: op.Name, Conflict: op.Conflict, Size: op.Size, Part: *op.Part})
	return name, ok && dir.Sync() == nil
}
func (w subprocessWriter) CleanupRecorded(ctx context.Context, s Subject, part partMetadata) error {
	dir, err := openDirectory(part.Destination)
	if err != nil {
		return err
	}
	defer dir.Close()
	got, err := fileIdentity(dir)
	if err != nil || got != part.Directory {
		return errors.New("cleanup directory changed")
	}
	return w.cleanupPart(ctx, moveSpec{Subject: s, Directory: part.Directory, Part: part}, dir)
}
