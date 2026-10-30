package delegate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"os"
	"syscall"
	"time"
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
	err := w.call(ctx, api, method, version, params, "", &out)
	return out, err
}

func (w JobWebAPI) call(ctx context.Context, api, method string, version int, params map[string]any, responseType string, out any) error {
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
	if w.JobID != "" {
		p["job_id"] = w.JobID
	}
	if w.callExecutor != nil {
		return w.callExecutor(ctx, "webapi.call", p, out)
	}
	return w.Client.Call(ctx, "webapi.call", nil, p, out)
}

// MaxWebAPIRawBytes is the approved raw response ceiling (2 GiB).
const MaxWebAPIRawBytes int64 = 2 << 30

// WebAPIRawTimeout bounds a raw transfer, independently of JSON WebAPI calls.
const WebAPIRawTimeout = 30 * time.Minute

// RawWebAPIResult describes the body written by CallRawTo after CGI header
// removal. The body is verified and staging is deleted before success.
type RawWebAPIResult struct {
	ContentType string
	Size        int64
	SHA256      string
}

// CallRawTo writes at most 2 GiB to dst using bounded memory and requires the
// profile's raw_max_bytes opt-in. It verifies size/hash and consumes private
// staging even on failure. dst may contain partial or unverified bytes on error:
// use a temporary file and publish it only after success. It never closes dst,
// retries a request, or follows redirects. A blocking dst must honor cancellation
// itself; context cancellation is checked between reads and writes.
func (w JobWebAPI) CallRawTo(ctx context.Context, api, method string, version int, params map[string]any, dst io.Writer) (RawWebAPIResult, error) {
	if dst == nil {
		return RawWebAPIResult{}, fail(CodeInvalidRequest)
	}
	var out struct {
		ResponseType string  `json:"response_type"`
		ContentType  string  `json:"content_type"`
		Size         int64   `json:"size"`
		SHA256       string  `json:"sha256"`
		Body         *string `json:"body"`
		Staging      string  `json:"staging"`
	}
	if err := w.call(ctx, api, method, version, params, "raw", &out); err != nil {
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
	if out.ResponseType != "raw" || out.ContentType == "" || len(out.ContentType) > 256 || out.Size < 0 || out.Size > MaxWebAPIRawBytes || !rootJobRE.MatchString(out.SHA256) || (out.Body == nil) == (out.Staging == "") {
		return RawWebAPIResult{}, fail(CodeExecutorProtocol)
	}
	if _, _, err := mime.ParseMediaType(out.ContentType); err != nil {
		return RawWebAPIResult{}, fail(CodeExecutorProtocol)
	}
	var src io.Reader
	if out.Body != nil {
		if out.Size > 32<<10 || len(*out.Body) > base64.StdEncoding.EncodedLen(32<<10) {
			return RawWebAPIResult{}, fail(CodeExecutorProtocol)
		}
		body, err := base64.StdEncoding.Strict().DecodeString(*out.Body)
		if err != nil || int64(len(body)) != out.Size {
			return RawWebAPIResult{}, fail(CodeExecutorProtocol)
		}
		src = bytes.NewReader(body)
	} else {
		prior, statErr := root.Lstat(out.Staging)
		if statErr != nil || !prior.Mode().IsRegular() {
			return RawWebAPIResult{}, fail(CodeExecutorProtocol)
		}
		f, openErr := root.OpenFile(out.Staging, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if openErr != nil {
			return RawWebAPIResult{}, fail(CodeExecutorProtocol)
		}
		defer f.Close()
		info, statErr := f.Stat()
		if statErr != nil || !os.SameFile(prior, info) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != out.Size {
			return RawWebAPIResult{}, fail(CodeExecutorProtocol)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) {
			return RawWebAPIResult{}, fail(CodeExecutorProtocol)
		}
		src = f
	}
	h := sha256.New()
	reader := contextReader{ctx, src}
	n, err := io.CopyBuffer(io.MultiWriter(dst, h), io.LimitReader(reader, out.Size), make([]byte, 32<<10))
	if err != nil {
		return RawWebAPIResult{}, err
	}
	var extra [1]byte
	excess, endErr := reader.Read(extra[:])
	if ctx.Err() != nil {
		return RawWebAPIResult{}, ctx.Err()
	}
	if n != out.Size || excess != 0 || endErr != io.EOF || hex.EncodeToString(h.Sum(nil)) != out.SHA256 {
		return RawWebAPIResult{}, fail(CodeExecutorProtocol)
	}
	return RawWebAPIResult{ContentType: out.ContentType, Size: out.Size, SHA256: out.SHA256}, nil
}
