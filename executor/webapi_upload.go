package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

// One synowebapi argv element is `name=<json>` and Linux refuses a single
// argument of 131072 bytes or more (MAX_ARG_STRLEN, counting the NUL). 96000
// bytes encode to exactly 128000 base64 bytes; with two JSON quotes, a name of
// at most 64 bytes, "=" and the NUL that is at most 128068, which fits.
const maxWebAPIUpload int64 = 96000
const maxWebAPIUploadBase64 = (maxWebAPIUpload + 2) / 3 * 4
const maxArgvString = 131072

var uploadHandle = regexp.MustCompile(`^app-[a-f0-9]{32}$`)
var errUpload = errors.New("WebAPI upload")

// webAPIUpload names App-staged bytes the executor injects as one param.
type webAPIUpload struct {
	Param   string `json:"param"`
	Staging string `json:"staging"`
	Size    *int64 `json:"size"`
	SHA256  string `json:"sha256"`
}

// UploadStore consumes App-staged upload bytes. Take verifies and removes the
// file; Discard removes it without reading (a refused request).
type UploadStore interface {
	Take(handle string, size, max int64, sum string) ([]byte, error)
	Discard(handle string)
}

// uploadDir is the package-private upload folder; every file must belong to
// the package account.
type uploadDir struct {
	Dir string
	UID int
}

func (u uploadDir) open() (*os.Root, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(u.Dir))
	if err != nil {
		return nil, err
	}
	path := filepath.Join(parent, filepath.Base(u.Dir))
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errUpload
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != u.UID {
		return nil, errUpload
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	if opened, err := root.Stat("."); err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, errUpload
	}
	return root, nil
}

func (u uploadDir) Discard(handle string) {
	if !uploadHandle.MatchString(handle) {
		return
	}
	if root, err := u.open(); err == nil {
		root.Remove(handle)
		root.Close()
	}
}

// Take unlinks the file whatever happens after the folder opens.
func (u uploadDir) Take(handle string, size, max int64, sum string) ([]byte, error) {
	if !uploadHandle.MatchString(handle) {
		return nil, errUpload
	}
	root, err := u.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	defer root.Remove(handle)
	if size < 0 || size > max {
		return nil, errUpload
	}
	f, err := root.OpenFile(handle, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errUpload
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != size {
		return nil, errUpload
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint64(st.Nlink) != 1 || int(st.Uid) != u.UID {
		return nil, errUpload
	}
	body, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(body)) != size {
		return nil, errUpload
	}
	h := sha256.Sum256(body)
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(h[:])), []byte(sum)) != 1 {
		return nil, errUpload
	}
	return body, nil
}

type uploadParamKey struct{}

// withUploadParam tells liveDSM which param carries injected upload bytes, so
// its recheck can hold that one value to the upload bound and everything else
// to the ordinary params bound.
func withUploadParam(ctx context.Context, param string) context.Context {
	return context.WithValue(ctx, uploadParamKey{}, param)
}

// checkWebAPIUploadParams runs the ordinary params rules with the upload value
// replaced by an empty string, after bounding that value separately.
func checkWebAPIUploadParams(ctx context.Context, params map[string]any) (map[string]any, bool) {
	name, ok := ctx.Value(uploadParamKey{}).(string)
	if !ok {
		return params, true
	}
	value, ok := params[name].(string)
	if !ok || int64(len(value)) > maxWebAPIUploadBase64 {
		return nil, false
	}
	check := make(map[string]any, len(params))
	for k, v := range params {
		check[k] = v
	}
	check[name] = ""
	return check, true
}

// validWebAPIUpload checks the request shape only; policy is checked after
// the grant. It does not touch the file.
func validWebAPIUpload(u *webAPIUpload, params map[string]any) bool {
	if u == nil || u.Size == nil || *u.Size < 0 || *u.Size > maxWebAPIUpload || !uploadHandle.MatchString(u.Staging) || !rootSHA.MatchString(u.SHA256) {
		return false
	}
	if _, dup := params[u.Param]; dup {
		return false
	}
	raw, err := json.Marshal(map[string]any{u.Param: ""})
	if err != nil {
		return false
	}
	_, code := webAPIParams(raw)
	return code == ""
}

var rootSHA = regexp.MustCompile(`^[a-f0-9]{64}$`)

func encodeUpload(body []byte) string { return base64.StdEncoding.EncodeToString(body) }
