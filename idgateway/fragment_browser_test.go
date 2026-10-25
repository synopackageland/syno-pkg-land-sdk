package idgateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// Run with SPL_CHROME_BIN set to a headless Chromium executable. This exercises
// native form submission and redirect fragment inheritance, which HTTP clients
// and the gateway's in-memory browser do not implement.
func TestMenuColdLaunchBrowserFragment(t *testing.T) {
	chrome := os.Getenv("SPL_CHROME_BIN")
	if chrome == "" {
		t.Skip("set SPL_CHROME_BIN for the browser navigation regression")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("browser navigation regression requires Node")
	}
	for _, tc := range []struct {
		path     string
		fallback bool
	}{
		{path: "/menu?view=files&sort=name#opened"},
		{path: "/#/files?folder=a%2Fb"},
		{path: "/menu?view=files#%E6%AA%94%E6%A1%88"},
		{path: "/menu?view=files"},
		{path: "/menu?view=files&sort=name#opened", fallback: true},
	} {
		t.Run(fmt.Sprintf("%s/fallback=%v", tc.path, tc.fallback), func(t *testing.T) {
			up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `<!doctype html><p id="result"></p><script>document.getElementById('result').textContent=location.pathname+location.search+location.hash</script>`)
			})
			front, clock := newFront(t, ModeNone, up, nil)
			app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == PingPath {
					if r.Header.Get("Cookie") != "" || r.Header.Get("X-SYNO-TOKEN") != "" || r.URL.RawQuery != "" {
						t.Error("ping received credentials or deep link data")
					}
					if tc.fallback {
						http.Error(w, "unreachable", http.StatusServiceUnavailable)
						return
					}
				}
				front.ServeHTTP(w, r)
			}))
			t.Cleanup(app.Close)
			appURL, _ := url.Parse(app.URL)
			appPort, _ := strconv.Atoi(appURL.Port())
			authorize := newCGI(&fakeDSM{user: "alice", token: "tok", addr: "127.0.0.1"})
			authorize.Verifier = verifierFunc(func(r *http.Request) (api.Identity, error) {
				cookie, err := r.Cookie("id")
				if err != nil || cookie.Value != "s1" || r.Header.Get("X-SYNO-TOKEN") != "tok" {
					return api.Identity{}, api.ErrNoSession
				}
				return api.Identity{User: "alice"}, nil
			})
			authorize.AppPorts = []int{appPort}
			authorize.Now = clock.now
			dsm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Has("SynoToken") || strings.Contains(r.RequestURI, "#") || strings.Contains(r.Referer(), "#") {
					t.Error("credential or fragment leaked into a server URL or referrer")
				}
				if r.Method == http.MethodPost {
					if err := r.ParseForm(); err != nil || len(r.PostForm) != 1 || r.PostForm.Get("SynoToken") != "tok" {
						t.Error("POST must contain only the session token")
					}
				}
				switch r.URL.Path {
				case "/start":
					http.SetCookie(w, &http.Cookie{Name: "id", Value: "s1", Path: "/"})
					http.Redirect(w, r, "/open.cgi", http.StatusFound)
				case "/webman/login.cgi":
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"SynoToken":"tok"}`)
				case "/open.cgi":
					if r.Method == http.MethodPost && !prepareMenuPost(w, r) {
						return
					}
					writeMenuPage(w, r, Config{Package: "App"}, MenuDecision{Kind: MenuRedirect, Origin: app.URL}, app.URL+tc.path, &menuAuth{}, false)
				default:
					authorize.ServeHTTP(w, r)
				}
			}))
			t.Cleanup(dsm.Close)
			dsmURL, _ := url.Parse(dsm.URL)
			front.cfg.DSMPorts.HTTP, _ = strconv.Atoi(dsmURL.Port())
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			args := []string{"fragment_browser.mjs", chrome, t.TempDir(), dsm.URL + "/start", tc.path}
			if tc.fallback {
				args = append(args, "fallback")
			}
			cmd := exec.CommandContext(ctx, node, args...)
			cmd.WaitDelay = 2 * time.Second
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Chromium: %v\n%s", err, out)
			}
		})
	}
}
