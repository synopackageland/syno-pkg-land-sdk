package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"time"
)

// Environment the packaging CLI gives a daemon that declares daemon.ingress.
const (
	IngressSocketEnv = "SYNOPKGLAND_INGRESS_SOCKET"
	IngressPathEnv   = "SYNOPKGLAND_INGRESS_PATH"
)

// DefaultIngressStreamTimeout matches the CLI's default nginx proxy timeout.
const DefaultIngressStreamTimeout = time.Hour

var (
	ingressSocket = regexp.MustCompile(`^/tmp/synopkgland-ingress-([A-Za-z][A-Za-z0-9]{1,63})\.sock$`)
)

type ingressOptions struct {
	request, stream time.Duration
}

// IngressOption adjusts ServeIngress.
type IngressOption func(*ingressOptions)

// IngressTimeouts sets the deadline of JSON and transfer requests and of raw
// HTTP routes (api.HandleHTTP), which may stream for longer.
func IngressTimeouts(request, stream time.Duration) IngressOption {
	return func(o *ingressOptions) { o.request, o.stream = request, stream }
}

// ServeIngress serves the App's handler, normally its api.Server inside the
// same SDK middleware as its CGI, on the daemon ingress socket until ctx is
// canceled. DSM's nginx proxies the declared path to that socket, so the
// handler sees the same browser requests as the CGI: the SDK still checks
// origin, App entry, DSM session and route permission for each one.
//
// The request path loses the ingress prefix ("/synopkgland/<package>/rpc" arrives as
// "/rpc"), so api.HandleHTTP prefixes match as under CGI. The client address
// is nginx's X-Real-IP, which nginx sets from the TCP peer; the socket is
// mode 0666 because nginx runs as another account, so another local account
// that connects to it directly can choose that address. It never replaces
// DSM session verification.
func ServeIngress(ctx context.Context, h http.Handler, opts ...IngressOption) error {
	if os.Getenv("GATEWAY_INTERFACE") != "" {
		return errors.New("api: ServeIngress runs in a daemon, not under CGI")
	}
	if h == nil {
		return errors.New("api: ServeIngress needs a handler")
	}
	o := ingressOptions{request: DefaultRequestTimeout, stream: DefaultIngressStreamTimeout}
	for _, opt := range opts {
		opt(&o)
	}
	if o.request <= 0 || o.stream <= 0 {
		return errors.New("api: ingress timeouts must be positive")
	}
	socket, prefix := os.Getenv(IngressSocketEnv), os.Getenv(IngressPathEnv)
	match := ingressSocket.FindStringSubmatch(socket)
	if len(match) != 2 || prefix != "/synopkgland/"+match[1]+"/" {
		return fmt.Errorf("api: %s and %s must name the same package's /synopkgland/<package>/ ingress", IngressSocketEnv, IngressPathEnv)
	}
	return serveIngress(ctx, socket, ingressHandler(h, prefix, o))
}

func serveIngress(ctx context.Context, socket string, h http.Handler) error {
	// A socket left by a stopped daemon is replaced; anything else refuses.
	if st, err := os.Lstat(socket); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("api: %s exists and is not a socket", socket)
		}
		if err := os.Remove(socket); err != nil {
			return err
		}
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0o666); err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(stop); err != nil {
			srv.Close()
		}
		<-done
		return nil
	}
}

func ingressHandler(h http.Handler, prefix string, o ingressOptions) http.Handler {
	return &routeWrapper{next: h, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		addr, err := netip.ParseAddr(r.Header.Get("X-Real-IP"))
		if err != nil || addr.Zone() != "" {
			http.Error(w, "missing client address", http.StatusBadRequest)
			return
		}
		r = r.Clone(r.Context())
		r.RemoteAddr = net.JoinHostPort(addr.Unmap().String(), "0")
		r.URL.Path = "/" + strings.TrimPrefix(r.URL.Path, prefix)
		r.URL.RawPath = ""
		r.RequestURI = r.URL.RequestURI()
		timeout := o.request
		if HTTPRouteMethod(h, r) != "" {
			timeout = o.stream
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		h.ServeHTTP(w, r.WithContext(ctx))
	})}
}
