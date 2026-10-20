package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// Routes registers every method of the App. Permissions still come only
// from permissions.json; Routes grants nothing.
func Routes(s *Session) []api.Route {
	routes := make([]api.Route, 0, len(s.Methods))
	group := &api.BatchGroup{Run: s.CallBatch}
	for _, method := range s.Methods {
		switch s.Transfers[method] {
		case "upload":
			routes = append(routes, api.HandleStream(method, s.upload(method)))
		case "download":
			routes = append(routes, api.HandleStream(method, s.download(method)))
		default:
			routes = append(routes, api.HandleRequestBatch(method, func(ctx context.Context, caller api.Caller, r *http.Request, params json.RawMessage) (any, error) {
				call := s.newCall(caller, r)
				defer call.spool.remove()
				return s.Call(ctx, method, params, call)
			}, group))
		}
	}
	return routes
}

// newCall is a verified request as the App and the services see it.
func (s *Session) newCall(caller api.Caller, r *http.Request) *Call {
	id := requestID()
	c := Context{User: caller.User(), Admin: caller.Admin(), Locale: locale(r), RequestID: id}
	return &Call{Context: c, Caller: caller, Request: r, spool: &spool{dir: s.Spool, id: id}}
}

var languageTag = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8}){0,3}$`)

// locale is the browser's first language as a plain BCP 47 tag, or "".
func locale(r *http.Request) string {
	first, _, _ := strings.Cut(r.Header.Get("Accept-Language"), ",")
	first, _, _ = strings.Cut(first, ";")
	first = strings.TrimSpace(first)
	if len(first) > 35 || !languageTag.MatchString(first) {
		return ""
	}
	return first
}

func requestID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
