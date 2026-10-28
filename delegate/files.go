package delegate

import (
	"context"
	"github.com/synopackageland/syno-pkg-land-sdk/filestation"
	"io"
	"os"
	"path"
	"strings"
)

type ListOptions = filestation.ListOptions
type JobContext struct {
	Client                          ExecutorClient
	callExecutor                    func(context.Context, string, map[string]any, any) error
	JobID, DelegationID, StagingDir string
	// UploadDir is the App-private folder CallUpload stages request bytes in.
	UploadDir string
}

func (j JobContext) Files() JobFiles { return JobFiles{j} }

// JobFiles is a background job's file client. Beyond filestation.Files it has
// the operations only the executor offers.
type JobFiles struct{ JobContext }

var _ filestation.Files = JobFiles{}

const MaxDownloadBytes int64 = 64 << 20

func (f JobFiles) call(ctx context.Context, op string, p map[string]any, out any) error {
	if f.JobID == "" || f.DelegationID == "" {
		return fail(CodeInvalidRequest)
	}
	p["job_id"] = f.JobID
	p["delegation_id"] = f.DelegationID
	if f.callExecutor != nil {
		return f.callExecutor(ctx, op, p, out)
	}
	return f.Client.Call(ctx, op, nil, p, out)
}
func (f JobFiles) List(ctx context.Context, p string, o ListOptions) (filestation.Page, error) {
	var out filestation.Page
	// The fixed executor registry accepts path only; do not invent wire options.
	if o != (ListOptions{}) {
		return out, fail("unsupported")
	}
	e := f.call(ctx, "files.list", map[string]any{"path": p}, &out)
	if e == nil && (out.Files == nil || out.Offset != 0 || out.Total < len(out.Files)) {
		e = fail(CodeExecutorProtocol)
	}
	return out, e
}
func (f JobFiles) Info(ctx context.Context, p string) (filestation.Entry, error) {
	var out struct {
		Files []filestation.Entry `json:"files"`
	}
	e := f.call(ctx, "files.info", map[string]any{"path": p}, &out)
	if e != nil {
		return filestation.Entry{}, e
	}
	if len(out.Files) != 1 || out.Files[0].Path != p {
		return filestation.Entry{}, fail(CodeExecutorProtocol)
	}
	return out.Files[0], nil
}
func (f JobFiles) CreateFolder(ctx context.Context, p, n string) error {
	return f.call(ctx, "files.createFolder", map[string]any{"path": p, "name": n}, nil)
}
func (f JobFiles) CheckPermission(ctx context.Context, p string) error {
	return f.call(ctx, "files.checkPermission", map[string]any{"path": p}, nil)
}
func validName(n string) bool {
	return n != "" && n != "." && n != ".." && len(n) <= 255 && !strings.ContainsAny(n, "/\\\x00\r\n")
}
func validPath(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsAny(p, "\x00\r\n\\") && len(p) <= 4096
}

// stagingRoot creates the private staging folder if needed and opens it.
func (f JobFiles) stagingRoot() (*os.Root, error) {
	if e := os.MkdirAll(f.StagingDir, 0700); e != nil {
		return nil, fail("STAGING_UNAVAILABLE")
	}
	info, e := os.Lstat(f.StagingDir)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, fail("STAGING_UNAVAILABLE")
	}
	root, e := os.OpenRoot(f.StagingDir)
	if e != nil {
		return nil, fail("STAGING_UNAVAILABLE")
	}
	return root, nil
}

// Write consumes at most size+1 bytes into a random private staging file, then
// asks the executor to create the destination without overwriting it. Cleanup
// runs on refusals, read failures, cancellation and uncertain responses too.
func (f JobFiles) Write(ctx context.Context, dir, name string, source io.Reader, size int64) error {
	if !validPath(dir) || !validName(name) || size < 0 || size > MaxDownloadBytes || source == nil {
		return fail(CodeInvalidRequest)
	}
	root, e := f.stagingRoot()
	if e != nil {
		return e
	}
	defer root.Close()
	// CreateTemp generates names internally and uses O_EXCL with mode 0600.
	staged, e := os.CreateTemp(f.StagingDir, "write-")
	if e != nil {
		return fail("STAGING_UNAVAILABLE")
	}
	base := path.Base(staged.Name())
	defer root.Remove(base)
	defer staged.Close()
	n, e := io.Copy(staged, io.LimitReader(contextReader{ctx, source}, size+1))
	if e != nil {
		if ctx.Err() != nil {
			return fail("canceled")
		}
		return fail("STAGING_UNAVAILABLE")
	}
	if n != size {
		return fail("limit-exceeded")
	}
	if e = staged.Sync(); e != nil {
		return fail("STAGING_UNAVAILABLE")
	}
	if e = staged.Close(); e != nil {
		return fail("STAGING_UNAVAILABLE")
	}
	return f.call(ctx, "files.write", map[string]any{"staging": base, "dest_dir": dir, "name": name}, nil)
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}

// Conflict is the copy and move policy for a name already taken in the
// destination: "" or "fail" refuses it, ConflictOverwrite replaces it.
type Conflict string

const ConflictOverwrite Conflict = "overwrite"

type DeleteOptions struct {
	// Recursive is required to delete a folder with its contents.
	Recursive bool
	// Permanent allows deleting where the shared folder has no recycle bin.
	Permanent bool
}

// TaskState is a File Station task as the executor reports it. State is
// running, finished, failed (with Error), canceled or unknown.
type TaskState struct {
	TaskID   string  `json:"task_id"`
	State    string  `json:"state"`
	Progress float64 `json:"progress"`
	Error    string  `json:"error,omitempty"`
}

// CreateFolderAll creates name in p, creating missing folders of p too.
func (f JobFiles) CreateFolderAll(ctx context.Context, p, n string) error {
	if !validPath(p) || !validName(n) {
		return fail(CodeInvalidRequest)
	}
	return f.call(ctx, "files.createFolder", map[string]any{"path": p, "name": n, "parents": true}, nil)
}

// Rename gives p the new name n in the same folder.
func (f JobFiles) Rename(ctx context.Context, p, n string) error {
	if !validPath(p) || !validName(n) {
		return fail(CodeInvalidRequest)
	}
	return f.call(ctx, "files.rename", map[string]any{"path": p, "name": n}, nil)
}

// Copy starts copying p into destDir; follow it with TaskStatus.
func (f JobFiles) Copy(ctx context.Context, p, destDir string, c Conflict) (TaskState, error) {
	return f.transfer(ctx, "files.copy", p, destDir, c)
}

// Move starts moving p into destDir; follow it with TaskStatus.
func (f JobFiles) Move(ctx context.Context, p, destDir string, c Conflict) (TaskState, error) {
	return f.transfer(ctx, "files.move", p, destDir, c)
}
func (f JobFiles) transfer(ctx context.Context, op, p, destDir string, c Conflict) (TaskState, error) {
	if !validPath(p) || !validPath(destDir) || (c != "" && c != "fail" && c != ConflictOverwrite) {
		return TaskState{}, fail(CodeInvalidRequest)
	}
	params := map[string]any{"path": p, "dest_dir": destDir}
	if c != "" {
		params["conflict"] = string(c)
	}
	return f.task(ctx, op, params)
}

// Delete starts deleting p, to the recycle bin unless o.Permanent allows otherwise.
func (f JobFiles) Delete(ctx context.Context, p string, o DeleteOptions) (TaskState, error) {
	if !validPath(p) {
		return TaskState{}, fail(CodeInvalidRequest)
	}
	params := map[string]any{"path": p}
	if o.Recursive {
		params["recursive"] = true
	}
	if o.Permanent {
		params["permanent"] = true
	}
	return f.task(ctx, "files.delete", params)
}

// TaskStatus reports a task this job started.
func (f JobFiles) TaskStatus(ctx context.Context, id string) (TaskState, error) {
	return f.follow(ctx, "files.taskStatus", id)
}

// CancelTask stops a task this job started.
func (f JobFiles) CancelTask(ctx context.Context, id string) (TaskState, error) {
	return f.follow(ctx, "files.taskCancel", id)
}
func (f JobFiles) follow(ctx context.Context, op, id string) (TaskState, error) {
	if id == "" || len(id) > 128 {
		return TaskState{}, fail(CodeInvalidRequest)
	}
	return f.task(ctx, op, map[string]any{"task_id": id})
}
func (f JobFiles) task(ctx context.Context, op string, params map[string]any) (TaskState, error) {
	var out TaskState
	if e := f.call(ctx, op, params, &out); e != nil {
		return TaskState{}, e
	}
	switch out.State {
	case "running", "finished", "failed", "canceled", "unknown":
	default:
		return TaskState{}, fail(CodeExecutorProtocol)
	}
	if out.TaskID == "" {
		return TaskState{}, fail(CodeExecutorProtocol)
	}
	return out, nil
}

// Read returns the content of the regular file p, at most max bytes. The
// executor copies it into staging as the user; Read creates the staging folder
// first and removes that copy.
func (f JobFiles) Read(ctx context.Context, p string, max int64) ([]byte, error) {
	if !validPath(p) || max <= 0 || max > MaxDownloadBytes {
		return nil, fail(CodeInvalidRequest)
	}
	root, e := f.stagingRoot()
	if e != nil {
		return nil, e
	}
	defer root.Close()
	var out struct {
		Staging string `json:"staging"`
		Size    int64  `json:"size"`
	}
	if e := f.call(ctx, "files.read", map[string]any{"path": p, "max_bytes": max}, &out); e != nil {
		return nil, e
	}
	if !validName(out.Staging) || !strings.HasPrefix(out.Staging, "read-") || out.Size < 0 || out.Size > max {
		return nil, fail(CodeExecutorProtocol)
	}
	defer root.Remove(out.Staging)
	staged, e := root.Open(out.Staging)
	if e != nil {
		return nil, fail(CodeExecutorProtocol)
	}
	defer staged.Close()
	b, e := io.ReadAll(io.LimitReader(staged, out.Size+1))
	if e != nil || int64(len(b)) != out.Size {
		return nil, fail(CodeExecutorProtocol)
	}
	return b, nil
}
