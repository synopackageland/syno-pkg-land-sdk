package filestation

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

const MaxBytes = 512 * 1024
const maxMetadata = 1024 * 1024
const PageSize = 100

// downloadPeek bounds how much of a DSM 7.1 download is inspected for an error envelope.
const downloadPeek = 4096

// Client has no cookie jar or credentials. The destination is startup-owned,
// literal loopback only; proxies, redirects and automatic retries are disabled.
type Client struct {
	endpoint string
	http     *http.Client
}

func NewClient(base string) (*Client, error) {
	u, e := url.Parse(base)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, fail("unavailable")
	}
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, DisableKeepAlives: true, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 32 * 1024, DisableCompression: true}
	return &Client{endpoint: strings.TrimRight(base, "/") + "/webapi/entry.cgi", http: &http.Client{Transport: tr, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func fail(code string) error {
	return &api.Error{Code: code, Message: "File Station operation refused"}
}
func wireError(code int) error {
	switch code {
	case 106, 107, 119:
		return fail("session_required")
	case 105, 403, 407, 408:
		return fail("denied")
	case 414, 1805:
		return fail("conflict")
	case 400:
		return fail("invalid-request")
	default:
		return fail("unavailable")
	}
}
func fields(family, method, version string) url.Values {
	return url.Values{"api": {family}, "method": {method}, "version": {version}}
}
func array(s string) string { b, _ := json.Marshal([]string{s}); return string(b) }
func validPath(p string) bool {
	return len(p) > 1 && len(p) <= 4096 && strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsAny(p, "\x00\r\n\\,\"")
}
func (c *Client) send(ctx context.Context, original *http.Request, body io.Reader, contentType string) (*http.Response, error) {
	if c == nil || original == nil {
		return nil, fail("unavailable")
	}
	cookies := original.CookiesNamed("id")
	token := original.Header.Get("X-SYNO-TOKEN")
	if len(cookies) != 1 || cookies[0].Value == "" || len(cookies[0].Value) > 4096 || len(token) > 4096 || strings.ContainsAny(token, "\r\n\x00") {
		return nil, fail("session_required")
	}
	method, endpoint := http.MethodPost, c.endpoint
	if contentType == "" {
		method = http.MethodGet
		encoded, err := io.ReadAll(body)
		if err != nil {
			return nil, fail("unavailable")
		}
		endpoint += "?" + string(encoded)
		body = nil
	}
	req, e := http.NewRequestWithContext(ctx, method, endpoint, body)
	if e != nil {
		return nil, fail("unavailable")
	}
	req.Header.Set("Content-Type", contentType)
	req.AddCookie(cookies[0])
	req.Header.Set("X-SYNO-TOKEN", token)
	// Never forward caller Host, Authorization, forwarding headers or URLs.
	resp, e := c.http.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, fail("canceled")
		}
		return nil, fail("unavailable")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fail("unavailable")
	}
	return resp, nil
}
func (c *Client) form(ctx context.Context, r *http.Request, v url.Values) (*http.Response, error) {
	if r == nil {
		return nil, fail("unavailable")
	}
	v.Set("SynoToken", r.Header.Get("X-SYNO-TOKEN"))
	return c.send(ctx, r, strings.NewReader(v.Encode()), "application/x-www-form-urlencoded")
}
func decode(resp *http.Response, into any) error {
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, maxMetadata+1))
	if e != nil || len(b) > maxMetadata {
		return fail("unavailable")
	}
	var envelope struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &envelope) != nil || envelope.Success == nil {
		return fail("unavailable")
	}
	if !*envelope.Success {
		return wireError(envelope.Error.Code)
	}
	if into != nil && json.Unmarshal(envelope.Data, into) != nil {
		return fail("unavailable")
	}
	return nil
}
func (c *Client) call(ctx context.Context, r *http.Request, v url.Values, into any) error {
	resp, e := c.form(ctx, r, v)
	if e != nil {
		return e
	}
	return decode(resp, into)
}

// Verify checks that loopback WebAPI resolves the same user as authenticate.cgi.
// Failure (including DSM IP-bound sessions) never falls back to admin credentials.
func (c *Client) Verify(ctx context.Context, r *http.Request, user string) error {
	var d struct {
		Username string `json:"username"`
	}
	if e := c.call(ctx, r, fields("SYNO.Core.NormalUser", "get", "1"), &d); e != nil {
		return e
	}
	if user == "" || d.Username != user {
		return fail("session_required")
	}
	return nil
}

type Entry struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	IsDir      bool   `json:"isdir"`
	Additional struct {
		Size int64 `json:"size"`
	} `json:"additional"`
}
type Page struct {
	Files  []Entry `json:"files"`
	Shares []Entry `json:"shares,omitempty"`
	Total  int     `json:"total"`
	Offset int     `json:"offset"`
}

type ListOptions struct {
	Offset        int    `json:"offset"`
	Limit         int    `json:"limit"`
	SortBy        string `json:"sortBy"`
	SortDirection string `json:"sortDirection"`
}

func (c *Client) page(ctx context.Context, r *http.Request, p string, opts ListOptions) (Page, error) {
	offset := opts.Offset
	if opts.Limit == 0 {
		opts.Limit = PageSize
	}
	if opts.SortBy == "" {
		opts.SortBy = "name"
	}
	if opts.SortDirection == "" {
		opts.SortDirection = "ASC"
	}
	if opts.Limit < 1 || opts.Limit > PageSize || (opts.SortBy != "name" && opts.SortBy != "size" && opts.SortBy != "type" && opts.SortBy != "mtime") || (opts.SortDirection != "ASC" && opts.SortDirection != "DESC") {
		return Page{}, fail("invalid-request")
	}
	var d Page
	if offset < 0 || offset > 1000000 || (p != "" && !validPath(p)) {
		return d, fail("invalid-request")
	}
	method := "list"
	if p == "" {
		method = "list_share"
	}
	v := fields("SYNO.FileStation.List", method, "2")
	v.Set("offset", strconv.Itoa(offset))
	v.Set("limit", strconv.Itoa(opts.Limit))
	v.Set("sort_by", opts.SortBy)
	v.Set("sort_direction", opts.SortDirection)
	v.Set("additional", `["size"]`)
	if p != "" {
		v.Set("folder_path", p)
	}
	e := c.call(ctx, r, v, &d)
	if d.Files == nil {
		d.Files = []Entry{}
	}
	if len(d.Files) > opts.Limit || len(d.Shares) > opts.Limit {
		return Page{}, fail("unavailable")
	}
	return d, e
}
func (c *Client) Discover(ctx context.Context, r *http.Request, offset int) (Page, error) {
	return c.page(ctx, r, "", ListOptions{Offset: offset})
}
func (c *Client) List(ctx context.Context, r *http.Request, p string, opts ListOptions) (Page, error) {
	if !validPath(p) {
		return Page{}, fail("invalid-request")
	}
	return c.page(ctx, r, p, opts)
}
func (c *Client) Stat(ctx context.Context, r *http.Request, p string) (Entry, error) {
	var d Page
	if !validPath(p) {
		return Entry{}, fail("invalid-request")
	}
	v := fields("SYNO.FileStation.List", "getinfo", "2")
	v.Set("path", array(p))
	v.Set("additional", `["size"]`)
	if e := c.call(ctx, r, v, &d); e != nil {
		return Entry{}, e
	}
	if len(d.Files) != 1 || d.Files[0].Path != p || d.Files[0].Name == "" {
		return Entry{}, fail("not_found")
	}
	return d.Files[0], nil
}

// Download owns an open response body; the caller must close it. No file bytes
// are buffered and JSON error envelopes never become successful content.
type Download struct {
	Body               io.ReadCloser
	ContentType        string
	ContentDisposition string
	ContentLength      int64
}

func (c *Client) DownloadStream(ctx context.Context, r *http.Request, p string) (Download, error) {
	if !validPath(p) {
		return Download{}, fail("invalid-request")
	}
	v := fields("SYNO.FileStation.Download", "download", "2")
	v.Set("path", array(p))
	v.Set("mode", "download")
	if c == nil {
		return Download{}, fail("unavailable")
	}
	transfer := *c
	httpClient := *c.http
	httpClient.Timeout = 20 * time.Minute
	transfer.http = &httpClient
	resp, e := transfer.send(ctx, r, strings.NewReader(v.Encode()), "")
	if e != nil {
		return Download{}, e
	}
	body := resp.Body
	raw := resp.Header.Values("Content-Disposition")
	if len(raw) == 0 {
		// DSM 7.1 sends no Content-Disposition at all. Only a short body that is
		// a complete DSM envelope is an error; everything else is the file.
		if body, e = unlessEnvelope(resp); e != nil {
			return Download{}, e
		}
	} else if disposition, _, _ := mime.ParseMediaType(raw[0]); len(raw) != 1 || disposition != "attachment" {
		e = decode(resp, nil)
		if e == nil {
			e = fail("unavailable")
		}
		return Download{}, e
	}
	return Download{Body: body, ContentType: resp.Header.Get("Content-Type"), ContentDisposition: resp.Header.Get("Content-Disposition"), ContentLength: resp.ContentLength}, nil
}

// unlessEnvelope returns the whole body as a stream unless it fits in the peek
// and decodes as a DSM envelope, which is refused and never becomes content.
func unlessEnvelope(resp *http.Response) (io.ReadCloser, error) {
	head, e := io.ReadAll(io.LimitReader(resp.Body, downloadPeek+1))
	if e != nil {
		resp.Body.Close()
		return nil, fail("read_failed")
	}
	if len(head) <= downloadPeek {
		var envelope struct {
			Success *bool `json:"success"`
		}
		if json.Unmarshal(head, &envelope) == nil && envelope.Success != nil {
			resp.Body = io.NopCloser(bytes.NewReader(head))
			if e = decode(resp, nil); e == nil {
				e = fail("unavailable")
			}
			return nil, e
		}
	}
	return struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), resp.Body), resp.Body}, nil
}

// Read is the bounded package-private job import, using the same stream.
func (c *Client) Read(ctx context.Context, r *http.Request, p string) ([]byte, error) {
	stream, e := c.DownloadStream(ctx, r, p)
	if e != nil {
		return nil, e
	}
	defer stream.Body.Close()
	b, e := io.ReadAll(io.LimitReader(stream.Body, MaxBytes+1))
	if e != nil {
		return nil, fail("read_failed")
	}
	if len(b) > MaxBytes {
		return nil, fail("limit-exceeded")
	}
	if stream.ContentLength >= 0 && stream.ContentLength != int64(len(b)) {
		return nil, fail("read_failed")
	}
	return bytes.Clone(b), nil
}
