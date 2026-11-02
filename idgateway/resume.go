package idgateway

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// resumeToken is carried verbatim through the DSM launcher. Public tokens
// name an index plus both durable revisions, not a caller-supplied hostname.
type resumeToken struct {
	Txn         string
	Path        bool
	Public      bool
	Port, Index int
	R1, R2      uint64
}

var resumeRevision = regexp.MustCompile(`^[0-9]{1,19}\.[0-9]{1,19}$`)

// parseResume is shared by open.cgi and authorize.cgi. Never round-trip these
// tokens through a float or reconstruct a token supplied by a launcher.
func parseResume(raw string) (resumeToken, bool) {
	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 || !txnPattern.MatchString(parts[0]) {
		return resumeToken{}, false
	}
	token := resumeToken{Txn: parts[0]}
	if len(parts) == 2 {
		if parts[1] == "p" {
			token.Path = true
			return token, true
		}
		port, err := strconv.Atoi(parts[1])
		if err != nil || !validPort(port, false) || strconv.Itoa(port) != parts[1] {
			return resumeToken{}, false
		}
		token.Port = port
		return token, true
	}
	if parts[1] != "x0" && parts[1] != "x1" || !resumeRevision.MatchString(parts[2]) {
		return resumeToken{}, false
	}
	rev := strings.Split(parts[2], ".")
	var err error
	token.R1, err = strconv.ParseUint(rev[0], 10, 64)
	if err != nil {
		return resumeToken{}, false
	}
	token.R2, err = strconv.ParseUint(rev[1], 10, 64)
	if err != nil {
		return resumeToken{}, false
	}
	token.Public, token.Index = true, int(parts[1][1]-'0')
	return token, true
}
func (t resumeToken) origin(e External) string {
	if !t.Public || t.R1 != e.R1 || t.R2 != e.R2 || t.Index >= len(e.Accept) {
		return ""
	}
	return e.Accept[t.Index]
}
func externalResume(txn string, index int, e External) string {
	return fmt.Sprintf("%s:x%d:%d.%d", txn, index, e.R1, e.R2)
}
func localAppOrigin(r *http.Request, port int) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(hostname(r.Host), strconv.Itoa(port))
}
func (h *AuthorizeCGI) localOrigin(r *http.Request) string {
	if h.IngressPath != "" {
		return requestOrigin(r)
	}
	if h.AppHTTPSPort != 0 {
		return "https://" + net.JoinHostPort(hostname(r.Host), strconv.Itoa(h.AppHTTPSPort))
	}
	if len(h.AppPorts) == 0 {
		return ""
	}
	port := h.AppPorts[0]
	if r.TLS != nil {
		port = h.AppPorts[len(h.AppPorts)-1]
	}
	return localAppOrigin(r, port)
}
func (h *AuthorizeCGI) external() External {
	if h.External == nil {
		return External{}
	}
	return h.External()
}

// prepareResume also accepts the launcher's resume-only query. A stale or
// malformed extended token falls back to ordinary local sign-in, never to an
// unvalidated external return address. false means a response was written.
func (h *AuthorizeCGI) prepareResume(w http.ResponseWriter, r *http.Request, e External) (*http.Request, bool) {
	query := r.URL.Query()
	values, present := query["resume"]
	if !present {
		return r, true
	}
	fallback := func() (*http.Request, bool) {
		if origin := h.localOrigin(r); origin != "" && validRequestAuthority(r) {
			http.Redirect(w, r, origin+ingressRoute(h.IngressPath, "/"), http.StatusFound)
		} else {
			http.Error(w, "invalid sign-in resume", 400)
		}
		return nil, false
	}
	if len(values) != 1 {
		return fallback()
	}
	token, ok := parseResume(values[0])
	if !ok {
		return fallback()
	}
	origin := ""
	if token.Path && h.IngressPath != "" {
		origin = requestOrigin(r)
	} else if token.Public {
		origin = token.origin(e)
	} else if slices.Contains(h.AppPorts, token.Port) {
		origin = localAppOrigin(r, token.Port)
	}
	if origin == "" {
		return fallback()
	}
	if txn, exists := query["txn"]; exists && (len(txn) != 1 || txn[0] != token.Txn) {
		return fallback()
	}
	if ret, exists := query["return"]; exists && (len(ret) != 1 || ret[0] != origin+ingressRoute(h.IngressPath, ReturnPath)) {
		return fallback()
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	query.Set("txn", token.Txn)
	query.Set("return", origin+ingressRoute(h.IngressPath, ReturnPath))
	clone.URL.RawQuery = query.Encode()
	return clone, true
}

func validReturnPath(ret *url.URL) bool { return validReturnPathAt(ret, "") }
func validReturnPathAt(ret *url.URL, prefix string) bool {
	return ret.User == nil && ret.Path == ingressRoute(prefix, ReturnPath) && ret.RawPath == "" && ret.RawQuery == "" && !ret.ForceQuery && ret.Fragment == "" && ret.Opaque == ""
}
