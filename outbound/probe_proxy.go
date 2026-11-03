package outbound

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
)

// Each probe owns a loopback proxy: HTTPS stays encrypted and certificate
// verification stays with yt-dlp. Every connection (including redirects and
// embedded manifests) uses the same checked, DNS-pinned public dial as App HTTP.
// Limits bound work, not total elapsed time. Cancellation closes all tunnels;
// callers must stop their subprocess group on Context cancellation
const ProbeProxyMaxBytes int64 = 32 << 20
const ProbeProxyMaxRequests int64 = 64
const ProbeProxyMaxConnections int64 = 64

// ProbeProxyOptions may lower the SDK ceilings. Zero selects the default.
// AllowURL can impose additional restrictions; it cannot admit a destination
// refused by the SDK. HTTPS exposes only the CONNECT authority to this callback.
type ProbeProxyOptions struct {
	MaxBytes, MaxRequests, MaxConnections int64
	AllowURL                              func(string) bool
}

func (o ProbeProxyOptions) limits() (ProbeProxyOptions, error) {
	for _, limit := range []struct {
		value *int64
		max   int64
	}{{&o.MaxBytes, ProbeProxyMaxBytes}, {&o.MaxRequests, ProbeProxyMaxRequests}, {&o.MaxConnections, ProbeProxyMaxConnections}} {
		if *limit.value == 0 {
			*limit.value = limit.max
		}
		if *limit.value < 1 || *limit.value > limit.max {
			return o, errors.New("outbound: invalid probe proxy limit")
		}
	}
	return o, nil
}

// StartProbeProxy owns an authenticated ephemeral IPv4 loopback proxy for a
// package-account subprocess. It serves only public HTTP requests and HTTPS
// CONNECT tunnels; Apps cannot supply a listener, handler or alternate dialer.
// Pass URL() to the child's explicit proxy option and Context() to its runner.
// Parent cancellation closes accepted, hijacked and upstream connections.
// This controls configured proxy traffic, not arbitrary sockets or child code.
func StartProbeProxy(ctx context.Context, options ProbeProxyOptions) (*ProbeProxy, error) {
	if os.Geteuid() == 0 {
		return nil, errors.New("outbound: run the probe proxy as the package account")
	}
	return startProbeProxy(ctx, options, net.DefaultResolver.LookupNetIP, (&net.Dialer{}).DialContext)
}

// ProbeProxy owns one probe's network lifetime and work budgets. URL contains
// an ephemeral credential: never log, persist or return it to an App client.
type ProbeProxy struct {
	url         string
	denied      atomic.Bool
	exhausted   atomic.Bool
	options     ProbeProxyOptions
	once        sync.Once
	done        chan struct{}
	mu          sync.Mutex
	conns       map[net.Conn]struct{}
	closing     bool
	accepted    atomic.Int64
	requests    atomic.Int64
	connections atomic.Int64
	bytes       atomic.Int64
	ctx         context.Context
	cancel      context.CancelFunc
	server      *http.Server
	transport   *http.Transport
}

// URL is the child's proxy URL, including its per-probe credential.
func (p *ProbeProxy) URL() string              { return p.url }
func (p *ProbeProxy) Context() context.Context { return p.ctx }
func (p *ProbeProxy) Denied() bool             { return p.denied.Load() }
func (p *ProbeProxy) Exhausted() bool          { return p.exhausted.Load() }
func (p *ProbeProxy) exhaust()                 { p.exhausted.Store(true); p.cancel() }

// Close is idempotent and stops every accepted connection, including tunnels.
func (p *ProbeProxy) Close() {
	p.once.Do(func() {
		p.cancel()
		p.mu.Lock()
		p.closing = true
		conns := make([]net.Conn, 0, len(p.conns))
		for c := range p.conns {
			conns = append(conns, c)
		}
		p.mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
		p.server.Close()
		p.transport.CloseIdleConnections()
		<-p.done
	})
}

func startProbeProxy(parent context.Context, options ProbeProxyOptions, lookup lookupFunc, dial dialFunc) (*ProbeProxy, error) {
	options, err := options.limits()
	if err != nil {
		return nil, err
	}
	if parent.Err() != nil {
		return nil, parent.Err()
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	p := &ProbeProxy{cancel: cancel, ctx: ctx, options: options, done: make(chan struct{}), conns: make(map[net.Conn]struct{})}
	boundedDial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if p.connections.Add(1) > options.MaxConnections {
			p.exhaust()
			return nil, errors.New("probe work limit")
		}
		return dial(ctx, network, address)
	}
	password := rand.Text()
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("probe:"+password))
	p.url = "http://probe:" + password + "@" + listener.Addr().String()
	checked := func(_ context.Context, network, address string) (net.Conn, error) {
		connection, err := dialPublic(ctx, network, address, lookup, boundedDial)
		if errors.Is(err, errDestination) {
			p.denied.Store(true)
		}
		if err != nil {
			return nil, err
		}
		stop := context.AfterFunc(ctx, func() { connection.Close() })
		return &probeConn{Conn: connection, proxy: p, stop: stop}, nil
	}
	p.transport = &http.Transport{Proxy: nil, DialContext: checked, DisableKeepAlives: true, MaxResponseHeaderBytes: 64 << 10}
	p.server = &http.Server{MaxHeaderBytes: 64 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	p.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(auth)) != 1 {
			w.Header().Set("Proxy-Authenticate", `Basic realm="probe"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		if p.requests.Add(1) > options.MaxRequests {
			p.exhaust()
			http.Error(w, "probe work limit", http.StatusBadGateway)
			return
		}
		raw := r.URL.String()
		if r.Method == http.MethodConnect {
			raw = "https://" + r.Host + "/"
		}
		if ValidateURL(raw) != nil || (options.AllowURL != nil && !options.AllowURL(raw)) {
			p.denied.Store(true)
			http.Error(w, "destination denied", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodConnect {
			upstream, err := checked(ctx, "tcp", r.Host)
			if err != nil {
				http.Error(w, "destination unavailable", http.StatusBadGateway)
				return
			}
			defer upstream.Close()
			local, buffered, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer local.Close()
			stop := context.AfterFunc(ctx, func() { local.Close() })
			defer stop()
			if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
				return
			}
			if buffered.Flush() != nil {
				return
			}
			done := make(chan struct{})
			go func() { io.Copy(upstream, buffered); upstream.Close(); close(done) }()
			io.Copy(local, upstream)
			local.Close()
			<-done
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		request := r.Clone(ctx)
		request.RequestURI = ""
		request.Header.Del("Proxy-Authorization")
		request.Header.Del("Proxy-Connection")
		response, err := p.transport.RoundTrip(request)
		if err != nil {
			http.Error(w, "destination unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	})
	p.server.ConnState = func(c net.Conn, state http.ConnState) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if state == http.StateNew {
			if p.accepted.Add(1) > options.MaxConnections {
				p.exhaust()
				c.Close()
				return
			}
			if p.closing {
				c.Close()
			} else {
				p.conns[c] = struct{}{}
			}
		}
		if state == http.StateClosed {
			delete(p.conns, c)
		}
	}
	go func() { defer close(p.done); p.server.Serve(listener); cancel() }()
	context.AfterFunc(ctx, p.Close)
	return p, nil
}

type probeConn struct {
	net.Conn
	proxy *ProbeProxy
	stop  func() bool
}

func (c *probeConn) Close() error { c.stop(); return c.Conn.Close() }
func (c *probeConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if c.proxy.bytes.Add(int64(n)) > c.proxy.options.MaxBytes {
		c.proxy.exhaust()
		return 0, errors.New("probe work limit")
	}
	return n, err
}
func (c *probeConn) Write(b []byte) (int, error) {
	if c.proxy.bytes.Add(int64(len(b))) > c.proxy.options.MaxBytes {
		c.proxy.exhaust()
		return 0, errors.New("probe work limit")
	}
	return c.Conn.Write(b)
}
