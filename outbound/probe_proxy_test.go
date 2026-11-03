package outbound

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProbeProxyRejectsPrivateDNSAndRedirect(t *testing.T) {
	lookups, dials := 0, 0
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		lookups++
		if lookups == 1 {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		dials++
		if address != "93.184.216.34:80" {
			t.Errorf("un-pinned address %s", address)
		}
		c, s := net.Pipe()
		go func() {
			defer s.Close()
			buf := make([]byte, 4096)
			s.Read(buf)
			s.Write([]byte("HTTP/1.1 302 Found\r\nLocation: http://rebound.example/file\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
		}()
		return c, nil
	}
	p, err := startProbeProxy(context.Background(), ProbeProxyOptions{}, lookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	u, _ := url.Parse(p.URL())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}}
	resp, err := client.Get("http://public.example/a")
	if err == nil {
		resp.Body.Close()
	}
	if !p.Denied() || lookups != 2 || dials != 1 {
		t.Fatalf("denied=%v lookup=%d dial=%d", p.Denied(), lookups, dials)
	}
}

func TestProbeProxyBlocksPrivateHTTPSAndMixedDNS(t *testing.T) {
	for _, raw := range []string{"https://127.0.0.1/secret", "https://mixed.example/secret"} {
		p, err := startProbeProxy(context.Background(), ProbeProxyOptions{}, func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.AddrFrom4([4]byte{10, 0, 0, 1})}, nil
		}, func(context.Context, string, string) (net.Conn, error) {
			t.Error("private destination dialed")
			return nil, os.ErrPermission
		})
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(p.URL())
		tr := &http.Transport{Proxy: http.ProxyURL(u)}
		c := &http.Client{Transport: tr}
		if resp, err := c.Get(raw); err == nil {
			resp.Body.Close()
			t.Error("HTTPS tunnel admitted")
		}
		if !p.Denied() {
			t.Error("refusal not recorded")
		}
		tr.CloseIdleConnections()
		p.Close()
	}
}

func TestProbeProxyWorkBoundsAndCancellation(t *testing.T) {
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	dial := func(context.Context, string, string) (net.Conn, error) {
		c, s := net.Pipe()
		go func() {
			defer s.Close()
			buf := make([]byte, 4096)
			s.Read(buf)
			s.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"))
		}()
		return c, nil
	}
	for _, limit := range []string{"requests", "bytes", "cancel"} {
		t.Run(limit, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p, err := startProbeProxy(ctx, ProbeProxyOptions{}, lookup, dial)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			u, _ := url.Parse(p.URL())
			tr := &http.Transport{Proxy: http.ProxyURL(u)}
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr}
			switch limit {
			case "requests":
				p.requests.Store(ProbeProxyMaxRequests)
			case "bytes":
				p.bytes.Store(ProbeProxyMaxBytes)
			case "cancel":
				cancel()
			}
			if resp, err := client.Get("http://public.example/a"); err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			if p.ctx.Err() == nil {
				t.Fatal("work/cancel did not end proxy context")
			}
		})
	}
}

func TestProbeProxyTunnelsPublicHTTPSAndGuardsRedirect(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://127.0.0.1/secret", http.StatusFound)
			return
		}
		io.WriteString(w, "manifest")
	}))
	defer origin.Close()
	p, err := startProbeProxy(context.Background(), ProbeProxyOptions{}, func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "93.184.216.34:443" {
			t.Errorf("un-pinned TLS destination %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(origin.URL, "https://"))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	u, _ := url.Parse(p.URL())
	// Trust only this in-process fixture's self-signed test certificate.
	tr := &http.Transport{Proxy: http.ProxyURL(u), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	response, err := client.Get("https://public.example/manifest")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "manifest" {
		t.Fatalf("%q %v", body, err)
	}
	if p.Denied() {
		t.Fatal("public TLS refused")
	}
	if response, err = client.Get("https://public.example/redirect"); err == nil {
		response.Body.Close()
		t.Fatal("private TLS redirect admitted")
	}
	if !p.Denied() {
		t.Fatal("private TLS redirect not recorded")
	}
}

func TestProbeProxyPublicContract(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := StartProbeProxy(ctx, ProbeProxyOptions{})
	if os.Geteuid() == 0 {
		if err == nil {
			p.Close()
			t.Fatal("root admitted")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(p.URL())
	if err != nil || u.Hostname() != "127.0.0.1" || u.User == nil {
		t.Fatal("proxy is not private authenticated loopback")
	}
	p.Close()
	p.Close()
	if p.Context().Err() == nil {
		t.Fatal("close did not cancel context")
	}
	for _, options := range []ProbeProxyOptions{{MaxRequests: ProbeProxyMaxRequests + 1}, {MaxConnections: ProbeProxyMaxConnections + 1}, {MaxBytes: ProbeProxyMaxBytes + 1}} {
		if p, err := StartProbeProxy(ctx, options); err == nil {
			p.Close()
			t.Fatal("unbounded configuration accepted")
		}
	}
}

func TestProbeProxyAuthenticationAndAdditionalPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		auth, allow  bool
	}{
		{"unauthenticated", "http://public.example/file", false, true},
		{"extra-policy", "http://public.example/file", true, false},
		{"cannot-relax-policy", "http://127.0.0.1/file", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := startProbeProxy(context.Background(), ProbeProxyOptions{AllowURL: func(string) bool { return tc.allow }},
				func(context.Context, string, string) ([]netip.Addr, error) {
					t.Error("unexpected DNS request")
					return nil, nil
				},
				func(context.Context, string, string) (net.Conn, error) { t.Error("unauthorized dial"); return nil, nil })
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			u, _ := url.Parse(p.URL())
			if !tc.auth {
				u.User = nil
			}
			tr := &http.Transport{Proxy: http.ProxyURL(u)}
			defer tr.CloseIdleConnections()
			response, err := (&http.Client{Transport: tr}).Get(tc.target)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			want := http.StatusForbidden
			if !tc.auth {
				want = http.StatusProxyAuthRequired
			}
			if response.StatusCode != want {
				t.Fatalf("status=%d", response.StatusCode)
			}
			if p.Denied() != tc.auth {
				t.Fatal("wrong refusal reason")
			}
		})
	}
}

func TestProbeProxyFiniteRedirectChainAndConnectionBudget(t *testing.T) {
	for _, connectionLimit := range []bool{false, true} {
		options := ProbeProxyOptions{MaxRequests: 3}
		if connectionLimit {
			options.MaxRequests = 64
			options.MaxConnections = 3
		}
		p, err := startProbeProxy(context.Background(), options,
			func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			},
			func(context.Context, string, string) (net.Conn, error) {
				c, s := net.Pipe()
				go func() {
					defer s.Close()
					buf := make([]byte, 4096)
					s.Read(buf)
					s.Write([]byte("HTTP/1.1 302 Found\r\nLocation: http://public.example/next\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
				}()
				return c, nil
			})
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(p.URL())
		tr := &http.Transport{Proxy: http.ProxyURL(u)}
		client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
		if response, err := client.Get("http://public.example/start"); err == nil {
			response.Body.Close()
		}
		if !p.Exhausted() || p.Context().Err() == nil {
			t.Fatal("unbounded redirect/connection chain")
		}
		tr.CloseIdleConnections()
		p.Close()
	}
}

func TestProbeProxyCancellationClosesActiveTunnel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	remote := make(chan net.Conn, 1)
	p, err := startProbeProxy(ctx, ProbeProxyOptions{},
		func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			c, s := net.Pipe()
			remote <- s
			return c, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	u, _ := url.Parse(p.URL())
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	password, _ := u.User.Password()
	auth := base64.StdEncoding.EncodeToString([]byte(u.User.Username() + ":" + password))
	fmt.Fprintf(conn, "CONNECT public.example:443 HTTP/1.1\r\nHost: public.example:443\r\nProxy-Authorization: Basic %s\r\n\r\n", auth)
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("CONNECT: %v %v", response, err)
	}
	upstream := <-remote
	defer upstream.Close()
	cancel()
	p.Close()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	upstream.SetReadDeadline(time.Now().Add(time.Second))
	for _, r := range []io.Reader{conn, upstream} {
		if _, err := r.Read(make([]byte, 1)); err == nil {
			t.Fatal("canceled tunnel stayed open")
		} else if n, ok := err.(net.Error); ok && n.Timeout() {
			t.Fatal("tunnel was not closed")
		}
	}
}

func TestProbeProxyDoesNotForwardItsCredential(t *testing.T) {
	seen := make(chan string, 1)
	p, err := startProbeProxy(context.Background(), ProbeProxyOptions{},
		func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			c, s := net.Pipe()
			go func() {
				defer s.Close()
				request, err := http.ReadRequest(bufio.NewReader(s))
				if err != nil {
					seen <- "bad request"
					return
				}
				seen <- request.Header.Get("Proxy-Authorization")
				s.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"))
			}()
			return c, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	u, _ := url.Parse(p.URL())
	tr := &http.Transport{Proxy: http.ProxyURL(u)}
	defer tr.CloseIdleConnections()
	response, err := (&http.Client{Transport: tr}).Get("http://public.example/file")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if auth := <-seen; auth != "" {
		t.Fatal("proxy credential reached origin")
	}
}
