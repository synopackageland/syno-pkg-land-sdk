package idgateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signedBrowser(t *testing.T, f *Front, c *clock) *browser {
	t.Helper()
	b := newBrowser(t, f)
	ret := dsmBounce(t, b.do("GET", "https://nas.example:18443/", navigate...), alice, c.t)
	if w := b.do("GET", ret, navigate...); w.Code != http.StatusFound {
		t.Fatalf("sign in: %d", w.Code)
	}
	return b
}

func TestComposeNoneKeepsNativeLoginAndFiltersOnlyReservedCookies(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		for _, v := range []string{"session=one; Path=/; HttpOnly; SameSite=Strict", "session=two; Path=/api", "id=replace; Path=/", "did=replace", "_SSID=replace", "_CrPoSt=replace", "stay_login=replace", "synopkgland_sso_App=replace", "synopkgland_sso_OtherApp=replace", "synopkgland_sso_OtherApp_b=replace"} {
			w.Header().Add("Set-Cookie", v)
		}
		if r.Header.Get("Remote-User") != "" || r.Header.Get("X-Syno-User") != "" {
			t.Error("none mode forwarded a claimed identity")
		}
		io.WriteString(w, r.Header.Get("Cookie"))
	})
	f, c := newFront(t, Mode("none"), up, nil)
	b := signedBrowser(t, f, c)
	w := b.do("POST", "https://nas.example:18443/login", "Origin", "https://nas.example:18443", "Cookie", "session=one; session=two; id=DSM; did=DEVICE; _SSID=LOGIN_CONTEXT; _CrPoSt=PORT_CONTEXT; stay_login=1; synopkgland_sso_OtherApp=OTHER; synopkgland_sso_OtherApp_b=NONCE", "Remote-User", "mallory", "X-Syno-User", "mallory")
	if w.Code != 200 || w.Body.String() != "session=one; session=two" {
		t.Fatalf("native login: %d cookies=%q", w.Code, w.Body.String())
	}
	if got := w.Header().Values("Set-Cookie"); len(got) != 2 || !strings.Contains(got[0], "HttpOnly; SameSite=Strict") {
		t.Fatalf("upstream cookies: %v", got)
	}
}

func TestComposeMutationsRequireExactBrowserOriginWithoutSynoToken(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []string
		status  int
	}{
		{"origin", []string{"Origin", "https://nas.example:18443"}, 200},
		{"referer", []string{"Referer", "https://nas.example:18443/login?x=1"}, 200},
		{"missing", nil, 403}, {"null", []string{"Origin", "null"}, 403},
		{"port", []string{"Origin", "https://nas.example:5001"}, 403},
		{"scheme", []string{"Origin", "http://nas.example:18443"}, 403},
		{"host", []string{"Origin", "https://evil.example:18443"}, 403},
		{"duplicate", []string{"Origin", "https://nas.example:18443", "Origin", "https://evil.example"}, 403},
		{"fetch-site", []string{"Origin", "https://nas.example:18443", "Sec-Fetch-Site", "same-site"}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := newUpstream(t, nil)
			f, c := newFront(t, ModeHeader, up, nil)
			b := signedBrowser(t, f, c)
			w := b.do("POST", "https://nas.example:18443/login", tc.headers...)
			if w.Code != tc.status {
				t.Fatalf("status %d want %d", w.Code, tc.status)
			}
			if tc.status == 403 && up.hits != 0 {
				t.Fatal("CSRF request reached upstream")
			}
		})
	}
}

func TestComposeReservedResponseCookieFilterAlsoAppliesToPassthrough(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "id=bad")
		w.Header().Add("Set-Cookie", "session=ok; Secure")
		w.WriteHeader(204)
	})
	f, _ := newFront(t, ModeHeader, up, nil)
	w := newBrowser(t, f).do("GET", "https://nas.example:18443/share/file")
	if got := w.Header().Values("Set-Cookie"); len(got) != 1 || got[0] != "session=ok; Secure" {
		t.Fatalf("cookies %v", got)
	}
}

func TestComposeWebSocketRejectsMissingOrForeignOriginBeforeUpstream(t *testing.T) {
	for _, origin := range []string{"", "https://other.example"} {
		up := newUpstream(t, nil)
		f, c := newFront(t, ModeHeader, up, nil)
		b := signedBrowser(t, f, c)
		w := b.do("GET", "https://nas.example:18443/socket", "Connection", "Upgrade", "Upgrade", "websocket", "Origin", origin)
		if w.Code != 403 || up.hits != 0 {
			t.Fatalf("origin=%q status=%d hits=%d", origin, w.Code, up.hits)
		}
	}
}

// Exercise streaming through a real HTTP server: ResponseRecorder cannot prove flush.
func TestComposeSSEFlushesBeforeTheUpstreamFinishes(t *testing.T) {
	finished := make(chan struct{})
	defer close(finished)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-finished:
		case <-r.Context().Done():
		}
	})
	f, _ := newFront(t, ModeHeader, up, func(c *FrontConfig) { c.Passthrough = []string{"/events"} })
	srv := httptest.NewServer(f)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b := make([]byte, len("data: ready\n\n"))
	if _, err := io.ReadFull(resp.Body, b); err != nil || string(b) != "data: ready\n\n" {
		t.Fatalf("SSE %q %v", b, err)
	}
}

func TestComposePublicNoneHasNoDSMAdmissionButStillGuardsBrowserMutations(t *testing.T) {
	up := newUpstream(t, nil)
	f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) { c.Access = "public" })
	if w := newBrowser(t, f).do("GET", "https://nas.example:18443/"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := newBrowser(t, f).do("POST", "https://nas.example:18443/login", "Origin", "https://evil.example"); w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestComposeSettingsNeverStartsUpstreamOrCreatesAnUnprotectedGrant(t *testing.T) {
	up := newUpstream(t, nil)
	f, c := newFront(t, ModeNone, up, func(c *FrontConfig) {
		c.ComposeBlocked = true
		c.Membership = &changingMembership{groups: []string{"administrators"}}
	})
	b := signedBrowser(t, f, c)
	w := b.do("GET", "https://nas.example:18443/")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "fixed package account") {
		t.Fatalf("settings %d %s", w.Code, w.Body)
	}
	w = b.do("POST", "https://nas.example:18443/_sdk/compose/confirm", "Origin", "https://nas.example:18443")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "COMPOSE_PROJECT_CONTROL_UNAVAILABLE") || up.hits != 0 {
		t.Fatalf("unsafe activation %d hits %d", w.Code, up.hits)
	}
}

func TestComposeUploadLimitsAreStreamingAndReport413(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			return
		}
		w.WriteHeader(204)
	})
	f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) { c.Access = "public"; c.MaxUploadBytes = 1024 })
	srv := httptest.NewServer(f)
	defer srv.Close()
	for _, chunked := range []bool{false, true} {
		req, _ := http.NewRequest("POST", srv.URL+"/upload", strings.NewReader(strings.Repeat("a", 2048)))
		req.Header.Set("Origin", srv.URL)
		if chunked {
			req.ContentLength = -1
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != 413 {
			t.Fatalf("chunked %v: got %d", chunked, res.StatusCode)
		}
	}
}

func TestComposeRedirectKeepsEncodedPathQueryAndFragment(t *testing.T) {
	up := newUpstream(t, nil)
	f, _ := newFront(t, ModeNone, up, nil)
	for _, suffix := range []string{"/login?next=a%2Fb#tab", "/?x=1#anchor"} {
		if got := f.publicURL(f.cfg.Upstream+suffix, "https://nas.example:18443"); got != "https://nas.example:18443"+suffix {
			t.Fatalf("redirect %q", got)
		}
	}
	for _, loc := range []string{"../login?x=1#tab", "/login#tab", "https://idp.example/signin"} {
		if got := f.publicURL(loc, "https://nas.example:18443"); got != loc {
			t.Fatal(got)
		}
	}
}

func TestComposeRangeAndHeadPreserveDownloadMetadata(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", "attachment; filename=file.bin")
		http.ServeContent(w, r, "file.bin", time.Unix(0, 0), strings.NewReader(strings.Repeat("0123456789", 1<<18)))
	})
	f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) { c.Access = "public" })
	srv := httptest.NewServer(f)
	defer srv.Close()
	for _, tc := range []struct {
		method, span string
		status       int
		body         string
	}{{"GET", "bytes=10-19", 206, "0123456789"}, {"GET", "bytes=9999999-", 416, ""}, {"HEAD", "", 200, ""}} {
		req, _ := http.NewRequest(tc.method, srv.URL+"/file", nil)
		req.Header.Set("Range", tc.span)
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != tc.status || res.Header.Get("Content-Disposition") == "" {
			t.Fatalf("download %+v", res)
		}
		if tc.status != 416 && string(body) != tc.body {
			t.Fatalf("body size %d", len(body))
		}
	}
}

func TestComposeWebSocketForwardsUpgradeAndBidirectionalFrames(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
		rw.Flush()
		frame := make([]byte, 8)
		if _, err := io.ReadFull(rw, frame); err != nil {
			t.Error(err)
			return
		}
		if frame[0] != 0x81 || frame[1] != 0x82 {
			t.Error("invalid browser frame")
			return
		}
		for i := 0; i < 2; i++ {
			frame[6+i] ^= frame[2+i%4]
		}
		rw.Write([]byte{0x81, 2, frame[6], frame[7]})
		rw.Flush()
	})
	f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) { c.Access = "public" })
	srv := httptest.NewServer(f)
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(conn, "GET /socket HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", addr, srv.URL)
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 101 {
		t.Fatal(res.StatusCode)
	}
	conn.Write([]byte{0x81, 0x82, 1, 2, 3, 4, 'h' ^ 1, 'i' ^ 2})
	frame := make([]byte, 4)
	if _, err = io.ReadFull(br, frame); err != nil || !bytes.Equal(frame, []byte{0x81, 2, 'h', 'i'}) {
		t.Fatalf("echo %v %v", frame, err)
	}
}

type changingMembership struct{ groups []string }

func (m *changingMembership) IsAdministrator(context.Context, string) (bool, error) {
	return slices.Contains(m.groups, "administrators"), nil
}

func (m *changingMembership) Groups(context.Context, string) ([]string, error) { return m.groups, nil }
func TestComposeAdmissionUsesFreshGroupMembership(t *testing.T) {
	up := newUpstream(t, nil)
	membership := &changingMembership{groups: []string{"allowed"}}
	f, c := newFront(t, ModeNone, up, func(c *FrontConfig) {
		c.Access = "dsm-groups"
		c.Groups = []string{"allowed"}
		c.Membership = membership
	})
	b := signedBrowser(t, f, c)
	if w := b.do("GET", "https://nas.example:18443/api"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	membership.groups = nil
	if w := b.do("GET", "https://nas.example:18443/api"); w.Code != 403 {
		t.Fatal("cached authorization", w.Code)
	}
}

type zeroStream struct{}

func (zeroStream) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestComposeLargeBodiesStreamWithoutWholeBodyBuffering(t *testing.T) {
	const size = int64(64 << 20)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			n, err := io.Copy(io.Discard, r.Body)
			if err != nil || n != size {
				t.Errorf("upload bytes=%d error=%v", n, err)
			}
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		io.CopyN(w, zeroStream{}, size)
	})
	f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) { c.Access = "public"; c.MaxUploadBytes = size + 1 })
	srv := httptest.NewServer(f)
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/upload", io.LimitReader(zeroStream{}, size))
	req.Header.Set("Origin", srv.URL)
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	res, err = srv.Client().Get(srv.URL + "/download")
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if err != nil || n != size {
		t.Fatalf("download bytes=%d error=%v", n, err)
	}
}
