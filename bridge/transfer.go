//go:build unix

package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/filestation"
)

// SpoolDir is the spool's name in the package's data directory: on the
// volume, not in DSM's small tmpfs /tmp.
const SpoolDir = "bridge-spool"

// MaxUploadBytes is the upload limit when the manifest sets none.
const MaxUploadBytes = filestation.MaxUploadBytes

// StaleSpool is the age after which a spool file is the leftover of a call
// the web server killed: longer than any request deadline (3600 s).
const StaleSpool = 2 * time.Hour

// spool holds one call's files: <dir>/<request ID>.<use>, 0600 in a 0700
// directory, removed when the call ends.
type spool struct {
	dir, id string
	opened  bool
	files   []string
}

// create makes a new, empty spool file for this call.
func (s *spool) create(use string) (*os.File, error) {
	if s == nil || s.dir == "" {
		return nil, errors.New("bridge: no spool directory")
	}
	if !s.opened {
		if err := openSpool(s.dir); err != nil {
			return nil, err
		}
		s.opened = true
	}
	name := filepath.Join(s.dir, s.id+"."+use)
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	s.files = append(s.files, name)
	return f, nil
}

func (s *spool) remove() {
	if s == nil {
		return
	}
	for _, name := range s.files {
		os.RemoveAll(name)
	}
}

// openSpool makes the spool directory, refuses one that is not a private
// directory of the package account, and removes what killed calls left.
func openSpool(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !ok || int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("bridge: spool %s is not a private directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > StaleSpool {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

// transferQuery is the URL query an App sees as params.query: one value per
// name, without the method and without anything that carries the session.
func transferQuery(r *http.Request) (map[string]string, error) {
	query := map[string]string{}
	for name, values := range r.URL.Query() {
		if name == "method" || strings.EqualFold(name, "SynoToken") || strings.EqualFold(name, "_sid") {
			continue
		}
		if len(values) != 1 {
			return nil, api.BadRequest("a query parameter is repeated")
		}
		query[name] = values[0]
	}
	return query, nil
}

func (s *Session) upload(method string) api.StreamHandler {
	return func(ctx context.Context, caller api.Caller, w http.ResponseWriter, r *http.Request) error {
		if r.Method != http.MethodPost {
			return &api.Error{Code: "method_not_allowed", Message: "use POST"}
		}
		if media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); media != "application/octet-stream" {
			return api.BadRequest("send Content-Type: application/octet-stream")
		}
		if r.ContentLength < 0 {
			return api.BadRequest("send Content-Length")
		}
		if r.ContentLength > s.maxUpload() {
			return &api.Error{Code: "request_too_large", Message: "the upload is larger than the App allows"}
		}
		query, err := transferQuery(r)
		if err != nil {
			return err
		}
		call := s.newCall(caller, r)
		defer call.spool.remove()
		f, err := call.spool.create("upload")
		if err != nil {
			return err
		}
		n, err := io.Copy(f, io.LimitReader(contextReader{ctx, r.Body}, r.ContentLength))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || n != r.ContentLength {
			return api.BadRequest("the upload ended before Content-Length bytes")
		}
		params, _ := json.Marshal(map[string]any{"query": query, "upload": map[string]any{"path": f.Name(), "size": n}})
		data, err := s.Call(ctx, method, params, call)
		if err != nil {
			return err
		}
		api.WriteData(w, data)
		return nil
	}
}

func (s *Session) download(method string) api.StreamHandler {
	return func(ctx context.Context, caller api.Caller, w http.ResponseWriter, r *http.Request) error {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			return &api.Error{Code: "method_not_allowed", Message: "use GET or POST"}
		}
		if media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); r.Method == http.MethodPost && media != "application/x-www-form-urlencoded" {
			return api.BadRequest("send a download form")
		}
		query, err := transferQuery(r)
		if err != nil {
			return err
		}
		call := s.newCall(caller, r)
		defer call.spool.remove()
		f, err := call.spool.create("download")
		if err != nil {
			return err
		}
		path := f.Name()
		f.Close()
		params, _ := json.Marshal(map[string]any{"query": query, "download": map[string]any{"path": path}})
		data, err := s.Call(ctx, method, params, call)
		if err != nil {
			return err
		}
		file, err := downloadFile(data)
		if err != nil {
			return err
		}
		// O_NONBLOCK: a FIFO in its place must not hold the request.
		body, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return fmt.Errorf("%w: download file: %v", ErrProtocol, err)
		}
		defer body.Close()
		info, err := body.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: download is not a regular file", ErrProtocol)
		}
		h := w.Header()
		h.Set("Content-Type", file.ContentType)
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
		h.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		// Once headers are sent a failed transfer stays truncated.
		if _, err := io.Copy(w, io.LimitReader(contextReader{ctx, body}, info.Size())); err != nil {
			log.Printf("bridge: download of %s interrupted: %v", method, err)
		}
		return nil
	}
}

// downloadAnswer is what a download method's App returns as data.
type downloadAnswer struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
}

func downloadFile(data json.RawMessage) (downloadAnswer, error) {
	var out downloadAnswer
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil || dec.More() {
		return out, fmt.Errorf("%w: download answer %s", ErrProtocol, data)
	}
	if out.ContentType == "" {
		out.ContentType = "application/octet-stream"
	}
	media, _, err := mime.ParseMediaType(out.ContentType)
	if !validFileName(out.Name) || err != nil || media == "" || len(out.ContentType) > 255 || strings.ContainsFunc(out.ContentType, unicode.IsControl) {
		return out, fmt.Errorf("%w: download answer %s", ErrProtocol, data)
	}
	return out, nil
}

func validFileName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 255 && utf8.ValidString(name) &&
		!strings.ContainsRune(name, '/') && !strings.ContainsFunc(name, unicode.IsControl)
}

// contextReader stops a transfer when the request ends.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func (s *Session) maxUpload() int64 {
	if s.MaxUploadBytes > 0 {
		return s.MaxUploadBytes
	}
	return MaxUploadBytes
}
