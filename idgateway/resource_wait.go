package idgateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// Bound active resource work, independently of App startup time. Additional
// resources wait for capacity; browser disconnect cancels that wait.
const resourceWaitLimit = 64

var errUpstreamBusy = errors.New("upstream work limit reached")

// Limit waiting response headers, then release the slot before streaming the
// body. Slow SSE/download/WebSocket bodies do not occupy header admission slots.
type boundedProxyTransport struct {
	base    *http.Transport
	headers chan struct{}
}

func (t *boundedProxyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	select {
	case t.headers <- struct{}{}:
		defer func() { <-t.headers }()
	default:
		return nil, errUpstreamBusy
	}
	return t.base.RoundTrip(r)
}

// Assets must queue behind active header work instead of failing page startup.
// Revalidate only after a slot is available: authority may change while queued.
func (t *boundedProxyTransport) resourceRoundTrip(r *http.Request, check func() error) (*http.Response, error) {
	select {
	case t.headers <- struct{}{}:
		defer func() { <-t.headers }()
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	if check != nil {
		if err := check(); err != nil {
			return nil, err
		}
	}
	return t.base.RoundTrip(r)
}

func isResourceRead(r *http.Request, ingress string) bool {
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || r.Body != nil && r.Body != http.NoBody || isUpgrade(r) || isNavigation(r) {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "script", "style", "font", "image", "manifest", "worker", "sharedworker", "serviceworker":
		return true
	}
	return strings.HasPrefix(r.URL.Path, ingress+"/assets/")
}

func waitResource(ctx context.Context) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type resourceAccessError struct{ code string }

func (e *resourceAccessError) Error() string { return e.code }

type resourceTransport struct {
	http.RoundTripper
	check func() error
}

func (t resourceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	for {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		var resp *http.Response
		var err error
		if bounded, ok := t.RoundTripper.(*boundedProxyTransport); ok {
			resp, err = bounded.resourceRoundTrip(r, t.check)
		} else {
			if t.check != nil {
				if err := t.check(); err != nil {
					return nil, err
				}
			}
			resp, err = t.RoundTripper.RoundTrip(r)
		}
		var op *net.OpError
		// Only retry a failed dial: no upstream connection existed, so no
		// request was delivered. Never replay a timeout after sending, an EOF,
		// an HTTP error, or a partially streamed response.
		if !errors.As(err, &op) || op.Op != "dial" || !(errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) || op.Timeout()) {
			return resp, err
		}
		if err := waitResource(r.Context()); err != nil {
			return nil, err
		}
	}
}
