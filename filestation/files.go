package filestation

import (
	"context"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"io"
	"net/http"
)

// Files is the operation set shared by live requests and delegated jobs.
// Construct a RequestContext at the authenticated CGI boundary; never persist it.
type Files interface {
	List(context.Context, string, ListOptions) (Page, error)
	Info(context.Context, string) (Entry, error)
	CreateFolder(context.Context, string, string) error
	CheckPermission(context.Context, string) error
	Write(context.Context, string, string, io.Reader, int64) error
}
type RequestContext struct {
	Caller  api.Caller
	Request *http.Request
	Service *Service
}

func (r RequestContext) Files() Files { return sessionFiles{r} }

type sessionFiles struct{ RequestContext }

func (s sessionFiles) List(ctx context.Context, p string, o ListOptions) (Page, error) {
	return s.Service.List(ctx, s.Caller, s.Request, p, o)
}
func (s sessionFiles) Info(ctx context.Context, p string) (Entry, error) {
	return s.Service.Info(ctx, s.Caller, s.Request, p)
}
func (s sessionFiles) CreateFolder(ctx context.Context, p, n string) error {
	return s.Service.CreateFolder(ctx, s.Caller, s.Request, p, n)
}
func (s sessionFiles) Write(ctx context.Context, p, n string, r io.Reader, size int64) error {
	return s.Service.Upload(ctx, s.Caller, s.Request, p, n, r, size)
}
func (s sessionFiles) CheckPermission(ctx context.Context, p string) error {
	return s.Service.CheckPermission(ctx, s.Caller, s.Request, p)
}
