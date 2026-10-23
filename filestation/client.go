package filestation

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

const MaxBytes = 512 * 1024
const PageSize = 100

// Client no longer forwards browser sessions to DSM over HTTP. Replaying a
// browser cookie from another address can revoke the browser's DSM session.
// Deprecated: use the browser SDK's same-origin File Station transport. Backend
// work needs an independently authorized adapter, such as an approved executor.
type Client struct{}

// NewClient retains source compatibility and destination validation, but never
// opens a connection. Every legacy operation returns unsupported before I/O.
func NewClient(base string) (*Client, error) {
	u, e := url.Parse(base)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, fail("unavailable")
	}
	return &Client{}, nil
}
func fail(code string) error {
	return &api.Error{Code: code, Message: "File Station operation refused"}
}
func validPath(p string) bool {
	return len(p) > 1 && len(p) <= 4096 && strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsAny(p, "\x00\r\n\\,\"")
}

func (*Client) Verify(context.Context, *http.Request, string) error { return fail("unsupported") }
func (*Client) Discover(context.Context, *http.Request, int) (Page, error) {
	return Page{}, fail("unsupported")
}
func (*Client) List(context.Context, *http.Request, string, ListOptions) (Page, error) {
	return Page{}, fail("unsupported")
}
func (*Client) Stat(context.Context, *http.Request, string) (Entry, error) {
	return Entry{}, fail("unsupported")
}
func (*Client) DownloadStream(context.Context, *http.Request, string) (Download, error) {
	return Download{}, fail("unsupported")
}
func (*Client) Read(context.Context, *http.Request, string) ([]byte, error) {
	return nil, fail("unsupported")
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

type Download struct {
	Body               io.ReadCloser
	ContentType        string
	ContentDisposition string
	ContentLength      int64
}
