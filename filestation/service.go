package filestation

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"io"
	"net/http"
	"strings"
	"time"
)

// DSMClient has only request-scoped session operations. It cannot open a NAS
// path, log in as a package account, or acquire an offline credential.
type DSMClient interface {
	Verify(context.Context, *http.Request, string) error
	Discover(context.Context, *http.Request, int) (Page, error)
	List(context.Context, *http.Request, string, ListOptions) (Page, error)
	Stat(context.Context, *http.Request, string) (Entry, error)
	Create(context.Context, *http.Request, string, string) error
	UploadStream(context.Context, *http.Request, string, string, io.Reader, int64) error
	DownloadStream(context.Context, *http.Request, string) (Download, error)
	Read(context.Context, *http.Request, string) ([]byte, error)
}
type Service struct{ client DSMClient }

func NewService(client DSMClient) *Service { return &Service{client: client} }

// Local uses the startup-owned loopback address; dataDir is never used to
// store sessions or infer NAS permissions.
func Local(_ string) *Service {
	c, e := NewClient("http://127.0.0.1:5000")
	if e != nil {
		return nil
	}
	return NewService(c)
}
func (s *Service) verify(ctx context.Context, c api.Caller, r *http.Request) error {
	if !c.Authenticated() {
		return fail("session_required")
	}
	if s == nil || s.client == nil {
		return fail("unavailable")
	}
	check, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return s.client.Verify(check, r, c.User())
}
func (s *Service) ListShares(ctx context.Context, c api.Caller, r *http.Request, offset int) (Page, error) {
	if e := s.verify(ctx, c, r); e != nil {
		return Page{}, e
	}
	return s.client.Discover(ctx, r, offset)
}
func (s *Service) List(ctx context.Context, c api.Caller, r *http.Request, path string, opts ListOptions) (Page, error) {
	if e := s.verify(ctx, c, r); e != nil {
		return Page{}, e
	}
	return s.client.List(ctx, r, path, opts)
}
func (s *Service) Info(ctx context.Context, c api.Caller, r *http.Request, path string) (Entry, error) {
	if e := s.verify(ctx, c, r); e != nil {
		return Entry{}, e
	}
	return s.client.Stat(ctx, r, path)
}
func (s *Service) CreateFolder(ctx context.Context, c api.Caller, r *http.Request, parent, name string) error {
	if e := s.verify(ctx, c, r); e != nil {
		return e
	}
	return s.client.Create(ctx, r, parent, name)
}
func (s *Service) Upload(ctx context.Context, c api.Caller, r *http.Request, parent, name string, source io.Reader, size int64) error {
	if e := s.verify(ctx, c, r); e != nil {
		return e
	}
	return s.client.UploadStream(ctx, r, parent, name, source, size)
}
func (s *Service) Download(ctx context.Context, c api.Caller, r *http.Request, path string) (Download, error) {
	if e := s.verify(ctx, c, r); e != nil {
		return Download{}, e
	}
	return s.client.DownloadStream(ctx, r, path)
}

// Routes exposes JSON metadata and HTTP streaming operations. Every entry
// passes through the central authenticator and immutable SDK policy.
func (s *Service) Routes() []api.Route {
	if s == nil {
		return nil
	}
	routes := []api.Route{}
	for _, method := range []string{"nas.session.status", "nas.files.shares", "nas.files.list", "nas.files.info", "nas.files.create_folder"} {
		name := method
		routes = append(routes, api.HandleRequest(name, func(ctx context.Context, c api.Caller, r *http.Request, p json.RawMessage) (any, error) {
			return s.handle(ctx, c, r, name, p)
		}))
	}
	return append(routes, api.HandleStream("nas.files.download", s.downloadHTTP), api.HandleStream("nas.files.upload", s.uploadHTTP))
}
func strict(p json.RawMessage, dst any) error {
	d := json.NewDecoder(bytes.NewReader(p))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil || d.Decode(new(any)) != io.EOF {
		return fail("invalid-request")
	}
	return nil
}
func (s *Service) handle(ctx context.Context, c api.Caller, r *http.Request, method string, p json.RawMessage) (any, error) {
	switch method {
	case "nas.session.status":
		if e := strict(p, &struct{}{}); e != nil {
			return nil, e
		}
		if e := s.verify(ctx, c, r); e != nil {
			return nil, e
		}
		return map[string]string{"mode": "filestation-session"}, nil
	case "nas.files.shares":
		var in struct {
			Offset int `json:"offset"`
		}
		if e := strict(p, &in); e != nil {
			return nil, e
		}
		return s.ListShares(ctx, c, r, in.Offset)
	case "nas.files.list":
		var in struct {
			Path string `json:"path"`
			ListOptions
		}
		if e := strict(p, &in); e != nil {
			return nil, e
		}
		return s.List(ctx, c, r, in.Path, in.ListOptions)
	case "nas.files.info":
		var in struct {
			Path string `json:"path"`
		}
		if e := strict(p, &in); e != nil {
			return nil, e
		}
		return s.Info(ctx, c, r, in.Path)
	case "nas.files.create_folder":
		var in struct {
			Parent string `json:"parent"`
			Name   string `json:"name"`
		}
		if e := strict(p, &in); e != nil {
			return nil, e
		}
		e := s.CreateFolder(ctx, c, r, in.Parent, in.Name)
		return map[string]bool{"created": e == nil}, e
	}
	return nil, fail("unsupported")
}

// relative constructs the existing job-import path; it is syntax validation,
// never a permission decision. DSM checks the resulting path with this session.
func relative(root, rel string) (string, error) {
	if !validPath(root) {
		return "", fail("invalid-request")
	}
	if rel == "" {
		return root, nil
	}
	for _, part := range strings.Split(rel, "/") {
		if !validName(part) {
			return "", fail("invalid-request")
		}
	}
	target := root + "/" + rel
	if !validPath(target) {
		return "", fail("invalid-request")
	}
	return target, nil
}

// readRegularFile avoids downloading a folder as an archive. DSM still enforces
// its current permissions on the subsequent download, not this metadata observation.
func (s *Service) readRegularFile(ctx context.Context, r *http.Request, target string) ([]byte, error) {
	item, e := s.client.Stat(ctx, r, target)
	if e != nil {
		return nil, e
	}
	if item.IsDir {
		return nil, fail("invalid-request")
	}
	if item.Additional.Size > MaxBytes {
		return nil, fail("limit-exceeded")
	}
	return s.client.Read(ctx, r, target)
}

// discover lists the share roots for an empty folder, else one folder's page.
func (s *Service) discover(ctx context.Context, r *http.Request, folder string, offset int) (Page, error) {
	if folder == "" {
		return s.client.Discover(ctx, r, offset)
	}
	return s.client.List(ctx, r, folder, ListOptions{Offset: offset})
}

// Browse lists share roots (empty folder) or one folder through the caller's
// own DSM session, so File Station's current permissions decide what is
// visible. It is read-only metadata and stores no grant; background jobs use it
// to let an authenticated caller pick the file that ReadFile will then import.
func (s *Service) Browse(ctx context.Context, c api.Caller, r *http.Request, folder string, offset int) (Page, error) {
	if !c.Authenticated() {
		return Page{}, fail("session_required")
	}
	if s == nil || s.client == nil {
		return Page{}, fail("unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if e := s.verify(ctx, c, r); e != nil {
		return Page{}, e
	}
	return s.discover(ctx, r, folder, offset)
}

// ReadFile reads one regular file below a folder the caller names, through the
// caller's own DSM session, up to MaxBytes. It is the single-file import used by
// package-private background jobs; the bytes never pass through the browser.
// The request itself is the consent, so no grant is stored.
func (s *Service) ReadFile(ctx context.Context, c api.Caller, r *http.Request, folder, rel string) ([]byte, error) {
	if !c.Authenticated() {
		return nil, fail("session_required")
	}
	if s == nil || s.client == nil {
		return nil, fail("unavailable")
	}
	if rel == "" {
		return nil, fail("invalid-request")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if e := s.verify(ctx, c, r); e != nil {
		return nil, e
	}
	item, e := s.client.Stat(ctx, r, folder)
	if e != nil {
		return nil, e
	}
	if !item.IsDir {
		return nil, fail("invalid-request")
	}
	target, e := relative(folder, rel)
	if e != nil {
		return nil, e
	}
	return s.readRegularFile(ctx, r, target)
}
