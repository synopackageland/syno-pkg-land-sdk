package delegate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"os"
	"syscall"
)

// WebAPIGrantOptions chooses a job-bound lifetime (1..2592000 seconds) or an
// until-revoked profile consent. It never contains a password or saved session.
type WebAPIGrantOptions struct {
	JobID        string
	TTLSeconds   int64
	UntilRevoked bool
}

// CreateWebAPIGrant uses only this caller's live auth, including remote_addr.
// Each later call rechecks the account, AppPriv and the approved profile pin.
func CreateWebAPIGrant(ctx context.Context, c ExecutorClient, auth map[string]string, profile string, o WebAPIGrantOptions) (Grant, error) {
	var out Grant
	p := map[string]any{"ops": []string{"webapi.call"}, "scope": profile}
	if o.UntilRevoked {
		if o.JobID != "" || o.TTLSeconds != 0 {
			return out, fail(CodeInvalidRequest)
		}
		p["lifetime"] = "until-revoked"
	} else {
		p["job_id"] = o.JobID
		p["ttl_seconds"] = o.TTLSeconds
	}
	err := c.Call(ctx, "delegate.create", auth, p, &out)
	return out, err
}

type JobWebAPI struct{ JobContext }

func (j JobContext) WebAPI() JobWebAPI { return JobWebAPI{j} }

// Call returns the full DSM envelope, including success:false. No mutation is
// retried. WEBAPI_UNCERTAIN or a transport failure after sending requires the
// App to reconcile the outcome before it offers a deliberate new operation.
func (w JobWebAPI) Call(ctx context.Context, api, method string, version int, params map[string]any) (json.RawMessage, error) {
	var out json.RawMessage
	err := w.call(ctx, api, method, version, params, "", nil, &out)
	return out, err
}

func (w JobWebAPI) call(ctx context.Context, api, method string, version int, params map[string]any, responseType string, upload map[string]any, out any) error {
	if w.DelegationID == "" {
		return fail(CodeInvalidRequest)
	}
	if params == nil {
		params = map[string]any{}
	}
	p := map[string]any{"delegation_id": w.DelegationID, "api": api, "method": method, "version": version, "params": params}
	if responseType != "" {
		p["response_type"] = responseType
	}
	if upload != nil {
		p["upload"] = upload
	}
	if w.JobID != "" {
		p["job_id"] = w.JobID
	}
	if w.callExecutor != nil {
		return w.callExecutor(ctx, "webapi.call", p, out)
	}
	return w.Client.Call(ctx, "webapi.call", nil, p, out)
}

// RawWebAPIResult contains the exact body after CGI header removal. Staged
// content has already been verified and deleted before CallRaw returns.
type RawWebAPIResult struct {
	Body        []byte
	ContentType string
	Size        int64
	SHA256      string
}

// CallRaw requires profile raw_max_bytes opt-in. It returns at most 64 MiB,
// verifies the size/hash for both transports, and consumes App-private staging.
// It never retries a request or follows a server redirect.
func (w JobWebAPI) CallRaw(ctx context.Context, api, method string, version int, params map[string]any) (RawWebAPIResult, error) {
	var out struct {
		ResponseType string  `json:"response_type"`
		ContentType  string  `json:"content_type"`
		Size         int64   `json:"size"`
		SHA256       string  `json:"sha256"`
		Body         *string `json:"body"`
		Staging      string  `json:"staging"`
	}
	if err := w.call(ctx, api, method, version, params, "raw", nil, &out); err != nil {
		return RawWebAPIResult{}, err
	}
	var root *os.Root
	if out.Staging != "" {
		if !rootFileRE.MatchString(out.Staging) || w.StagingDir == "" {
			return RawWebAPIResult{}, fail(CodeExecutorProtocol)
		}
		info, err := os.Lstat(w.StagingDir)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return RawWebAPIResult{}, fail("STAGING_UNAVAILABLE")
		}
		root, err = os.OpenRoot(w.StagingDir)
		if err != nil {
			return RawWebAPIResult{}, fail("STAGING_UNAVAILABLE")
		}
		defer root.Close()
		defer root.Remove(out.Staging)
		opened, err := root.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			return RawWebAPIResult{}, fail("STAGING_UNAVAILABLE")
		}
	}
	if out.ResponseType != "raw" || out.ContentType == "" || len(out.ContentType) > 256 || out.Size < 0 || out.Size > 64<<20 || !rootJobRE.MatchString(out.SHA256) || (out.Body == nil) == (out.Staging == "") {
		return RawWebAPIResult{}, fail(CodeExecutorProtocol)
	}
	if _, _, err := mime.ParseMediaType(out.ContentType); err != nil {
		return RawWebAPIResult{}, fail(CodeExecutorProtocol)
	}
	var body []byte
	var err error
	if out.Body != nil {
		if out.Size > 32<<10 {
			return RawWebAPIResult{}, fail(CodeExecutorProtocol)
		}
		body, err = base64.StdEncoding.Strict().DecodeString(*out.Body)
	} else {
		f, openErr := root.OpenFile(out.Staging, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if openErr != nil {
			return RawWebAPIResult{}, fail(CodeExecutorProtocol)
		}
		defer f.Close()
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != out.Size {
			return RawWebAPIResult{}, fail(CodeExecutorProtocol)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) {
			return RawWebAPIResult{}, fail(CodeExecutorProtocol)
		}
		body, err = io.ReadAll(io.LimitReader(contextReader{ctx, f}, out.Size+1))
	}
	if err != nil || int64(len(body)) != out.Size {
		return RawWebAPIResult{}, fail(CodeExecutorProtocol)
	}
	h := sha256.Sum256(body)
	if hex.EncodeToString(h[:]) != out.SHA256 {
		return RawWebAPIResult{}, fail(CodeExecutorProtocol)
	}
	return RawWebAPIResult{Body: body, ContentType: out.ContentType, Size: out.Size, SHA256: out.SHA256}, nil
}

// MaxWebAPIUploadBytes is the largest CallUpload body: 96000 bytes encode to
// 128000 base64 bytes, which stays under Linux's 131072-byte limit on the one
// argv string synowebapi receives for the parameter.
const MaxWebAPIUploadBytes = 96000

// CallUpload requires profile upload_max_bytes opt-in. It stages data in the
// App-private UploadDir (0700; file 0600), asks the executor to send it as the
// base64 value of param, and removes the file whatever happens. param must not
// be in params. The response is handled exactly as Call. It never retries.
func (w JobWebAPI) CallUpload(ctx context.Context, api, method string, version int, params map[string]any, param string, data []byte) (json.RawMessage, error) {
	if param == "" || len(data) > MaxWebAPIUploadBytes || w.UploadDir == "" {
		return nil, fail(CodeInvalidRequest)
	}
	if _, dup := params[param]; dup {
		return nil, fail(CodeInvalidRequest)
	}
	if err := os.MkdirAll(w.UploadDir, 0700); err != nil {
		return nil, fail("STAGING_UNAVAILABLE")
	}
	info, err := os.Lstat(w.UploadDir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, fail("STAGING_UNAVAILABLE")
	}
	root, err := os.OpenRoot(w.UploadDir)
	if err != nil {
		return nil, fail("STAGING_UNAVAILABLE")
	}
	defer root.Close()
	if opened, err := root.Stat("."); err != nil || !os.SameFile(info, opened) {
		return nil, fail("STAGING_UNAVAILABLE")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, fail(CodeInvalidRequest)
	}
	name := "app-" + hex.EncodeToString(id)
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fail("STAGING_UNAVAILABLE")
	}
	defer root.Remove(name)
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fail("STAGING_UNAVAILABLE")
	}
	sum := sha256.Sum256(data)
	upload := map[string]any{"param": param, "staging": name, "size": len(data), "sha256": hex.EncodeToString(sum[:])}
	var out json.RawMessage
	err = w.call(ctx, api, method, version, params, "", upload, &out)
	return out, err
}
