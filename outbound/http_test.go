package outbound

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestForbiddenDestinations(t *testing.T) {
	for _, host := range []string{"localhost", "localhost.", "service.localhost", "127.0.0.1", netip.AddrFrom4([4]byte{10, 1, 2, 3}).String(), netip.AddrFrom4([4]byte{172, 16, 0, 1}).String(), netip.AddrFrom4([4]byte{192, 168, 1, 1}).String(), "169.254.169.254", "100.100.100.200", "0.0.0.0", "224.0.0.1", "[::1]", "[::ffff:127.0.0.1]", "[fe80::1]", "[fc00::1]", "[64:ff9b::a9fe:a9fe]", "[2002:7f00:1::]"} {
		if err := ValidateURL("http://" + host + "/file"); err == nil {
			t.Errorf("accepted %s", host)
		}
	}
	for _, raw := range []string{"file:///etc/passwd", "https://user:secret@example.com/", "https://example.com/#fragment", "http://example.com:0/", "http://example.com:65536/"} {
		if ValidateURL(raw) == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"https://example.com/file", "https://8.8.8.8/file", "https://[2606:4700:4700::1111]/"} {
		if err := ValidateURL(raw); err != nil {
			t.Errorf("refused public URL: %s: %v", raw, err)
		}
	}
}

func TestResolutionAndPinnedDial(t *testing.T) {
	for _, answers := range [][]netip.Addr{
		nil, {netip.MustParseAddr("127.0.0.1")},
		{netip.MustParseAddr("8.8.8.8"), netip.AddrFrom4([4]byte{10, 0, 0, 1})},
		{netip.MustParseAddr("::ffff:169.254.169.254")},
	} {
		dialed := false
		_, err := dialPublic(context.Background(), "tcp", "example.com:443",
			func(context.Context, string, string) ([]netip.Addr, error) { return answers, nil },
			func(context.Context, string, string) (net.Conn, error) { dialed = true; return nil, errors.New("dial") })
		if err == nil || dialed {
			t.Fatalf("unsafe DNS answers reached dial: %v", answers)
		}
	}
	lookups := 0
	for attempt := 0; attempt < 2; attempt++ {
		dialed := false
		_, err := dialPublic(context.Background(), "tcp", "example.com:443",
			func(context.Context, string, string) ([]netip.Addr, error) {
				lookups++
				if lookups > 1 {
					return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
				}
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			},
			func(_ context.Context, network, address string) (net.Conn, error) {
				dialed = true
				if network != "tcp" || address != "8.8.8.8:443" {
					t.Fatalf("hostname resolved again: %s %s", network, address)
				}
				return nil, errors.New("dial")
			})
		if err == nil || dialed != (attempt == 0) {
			t.Fatal("rebound DNS was accepted", err, dialed)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRedirectCannotEnterPrivateNetwork(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: guardedTransport{roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"http://169.254.169.254/latest/"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}}
	if _, err := client.Get("https://example.com/file"); err == nil || calls != 1 {
		t.Fatal("unsafe redirect followed", err, calls)
	}
}

func TestDefaultTransportDoesNotReachLoopback(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer srv.Close()
	client := &http.Client{Transport: NewTransport(time.Second), Timeout: time.Second}
	if _, err := client.Get(srv.URL); err == nil || reached {
		t.Fatal("loopback reached", err)
	}
}
