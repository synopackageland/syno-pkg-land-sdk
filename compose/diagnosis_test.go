package compose

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const diagnosisDigest = "@sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func diagnosisProject(t *testing.T, refs ...string) string {
	t.Helper()
	images := make([]Image, len(refs))
	for i, ref := range refs {
		images[i] = Image{Service: fmt.Sprint(i), Source: "unused.example/app:tag", Reference: ref, Pinned: true}
	}
	data, _ := json.Marshal(map[string]any{"schema": 1, "package": "Fixture", "images": images})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.lock.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRegistryCheckUsesOnlyLockedReferencesAndNeverFollowsRedirects(t *testing.T) {
	var destination atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destination.Add(1) }))
	defer other.Close()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.String() != "/v2/" || r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "https://")
	project := diagnosisProject(t, host+"/app"+diagnosisDigest, host+"/second"+diagnosisDigest)
	results := checkRegistries(context.Background(), project, "postinst", server.Client().Transport)
	if len(results) != 1 || results[0].Cause != ImageUnknown || results[0].RegistryHost != host {
		t.Fatalf("%+v", results)
	}
	if requests.Load() != 1 || destination.Load() != 0 {
		t.Fatalf("requests=%d redirected=%d", requests.Load(), destination.Load())
	}
}

func TestRegistryCheckChallengeIsReachableButFailureStatusesAreLabelled(t *testing.T) {
	for _, tc := range []struct {
		status int
		cause  ImageCause
	}{{200, ""}, {401, ""}, {403, ImageUnknown}, {404, ImageUnknown}, {429, ImageRateLimit}, {503, ImageUnreachable}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="https://other.example/token"`)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, "password=never-save-this")
			}))
			defer server.Close()
			results := checkRegistries(context.Background(), diagnosisProject(t, strings.TrimPrefix(server.URL, "https://")+"/app"+diagnosisDigest), "prestart", server.Client().Transport)
			if tc.cause == "" {
				if len(results) != 0 {
					t.Fatal(results)
				}
				return
			}
			if len(results) != 1 || results[0].Cause != tc.cause || results[0].Source != "registry_check" || results[0].Phase != "prestart" {
				t.Fatalf("%+v", results)
			}
			raw, _ := json.Marshal(results)
			if strings.Contains(string(raw), "never-save") {
				t.Fatal("response leaked")
			}
			if _, err := time.Parse(time.RFC3339Nano, results[0].ObservedAt); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRegistryCheckTLSAndDeadlineAreRealTransportFailures(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	project := diagnosisProject(t, strings.TrimPrefix(server.URL, "https://")+"/app"+diagnosisDigest)
	results := checkRegistries(context.Background(), project, "postinst", http.DefaultTransport)
	if len(results) != 1 || results[0].Cause != ImageTLSTrust {
		t.Fatalf("%+v", results)
	}
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer slow.Close()
	// Prepare the filesystem fixture before spending the network timeout budget.
	slowProject := diagnosisProject(t, strings.TrimPrefix(slow.URL, "https://")+"/app"+diagnosisDigest)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	begin := time.Now()
	results = checkRegistries(ctx, slowProject, "preinst", slow.Client().Transport)
	if len(results) != 1 || results[0].Cause != ImageUnreachable || time.Since(begin) > time.Second {
		t.Fatalf("%+v elapsed=%s", results, time.Since(begin))
	}
}

func TestRegistryLockRejectsUnsafeInputsBeforeAnyNetwork(t *testing.T) {
	cases := []string{
		`{"schema":1,"package":"Fixture","images":[{"reference":"registry.example/app:latest","pinned":true}]}`,
		`{"schema":1,"package":"Fixture","images":[{"reference":"https://user:secret@registry.example/app` + diagnosisDigest + `","pinned":true}]}`,
		`{"schema":2,"package":"Fixture","images":[]}`,
		`{"schema":1,"package":"Fixture","images":[]}`, "not-json",
	}
	for _, raw := range cases {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "compose.lock.json"), []byte(raw), 0600)
		results := CheckRegistries(context.Background(), dir, "postinst")
		if len(results) != 1 || results[0].Cause != ImageUnknown || results[0].RegistryHost != "" {
			t.Fatalf("%s: %+v", raw, results)
		}
		b, _ := json.Marshal(results)
		if strings.Contains(string(b), "secret") {
			t.Fatal("lock data leaked")
		}
	}
	dir := t.TempDir()
	os.Symlink(filepath.Join(diagnosisProject(t, "registry.example/app"+diagnosisDigest), "compose.lock.json"), filepath.Join(dir, "compose.lock.json"))
	if got := CheckRegistries(context.Background(), dir, "preinst"); len(got) != 1 || got[0].Cause != ImageUnknown {
		t.Fatal(got)
	}
	refs := []string{}
	for i := 0; i < 5; i++ {
		refs = append(refs, fmt.Sprintf("registry%d.example/app%s", i, diagnosisDigest))
	}
	if got := CheckRegistries(context.Background(), diagnosisProject(t, refs...), "preinst"); len(got) != 1 || got[0].RegistryHost != "" {
		t.Fatal(got)
	}
}

func TestImageFailureClassificationDoesNotInferFromWrapperCodes(t *testing.T) {
	for _, tc := range []struct {
		message string
		cause   ImageCause
	}{
		{"x509: certificate signed by unknown authority", ImageTLSTrust},
		{"lookup registry.example: no such host", ImageDNS},
		{"dial tcp: connection refused", ImageUnreachable},
		{"toomanyrequests: download limit", ImageRateLimit},
		{"write /layers: no space left on device", ImageDiskFull},
		{"manifest unknown: manifest unknown", ImageNotFound},
		{"pull access denied", ImageDenied},
		{"fail compose up UUID: 2201 276 4540 4551 4580", ImageUnknown},
		{"services.app.command must be a string", ImageUnknown},
	} {
		if got := ClassifyImageFailure(tc.message); got != tc.cause {
			t.Fatalf("%q: %s", tc.message, got)
		}
	}
	err := &url.Error{Op: "Get", URL: "https://registry.example/v2/", Err: tls.RecordHeaderError{Msg: "not TLS"}}
	if classifyRegistryError(err) != ImageUnknown {
		t.Fatal("unproven TLS trust failure")
	}
}

// An offline NAS that already runs the locked images must not show a registry
// error on every start or same-image upgrade.
func TestRegistryCheckSkipsImagesThisPackageAlreadyRan(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "https://")
	ran := host + "/app" + diagnosisDigest
	pkgvar := t.TempDir()
	os.Mkdir(filepath.Join(pkgvar, "daemon"), 0700)
	t.Setenv("SYNOPKG_PKGVAR", pkgvar)
	record, _ := json.Marshal(map[string]any{"schema": 1, "package": "Fixture", "images": []string{ran}})
	if err := os.WriteFile(filepath.Join(pkgvar, "daemon", "compose-images.json"), record, 0600); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"postinst", "postupgrade", "prestart"} {
		if results := checkRegistries(context.Background(), diagnosisProject(t, ran), phase, server.Client().Transport); len(results) != 0 {
			t.Fatalf("%s: %+v", phase, results)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("registry contacted for images already present: %d", requests.Load())
	}
	// A new digest still needs the registry, so it is still checked.
	other := host + "/app@sha256:" + strings.Repeat("b", 64)
	if results := checkRegistries(context.Background(), diagnosisProject(t, ran, other), "postupgrade", server.Client().Transport); len(results) != 1 || requests.Load() != 1 {
		t.Fatalf("new image not checked: %+v requests=%d", results, requests.Load())
	}
}

// Docker mirrors apply to Docker Hub only, and any reachable path is sufficient
// for this informational check. No failed base probe proves image denial.
func TestRegistryCheckMirrorFallback(t *testing.T) {
	for _, tc := range []struct {
		name, image                  string
		mirrorStatus, upstreamStatus int
		wantFailure                  bool
	}{
		{"blocked-upstream", "docker.io/library/app", 200, 403, false},
		{"challenge", "docker.io/library/app", 401, 403, false},
		{"upstream-fallback", "docker.io/library/app", 503, 200, false},
		{"both-fail", "docker.io/library/app", 403, 403, true},
		{"unrelated-registry", "other.example/app", 200, 403, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mirrorCalls atomic.Int32
			transport := registryRoundTripper(func(r *http.Request) (*http.Response, error) {
				status := tc.upstreamStatus
				if r.URL.Host == "mirror.example" {
					mirrorCalls.Add(1)
					status = tc.mirrorStatus
				}
				return &http.Response{StatusCode: status, Body: http.NoBody, Header: http.Header{}}, nil
			})
			project := diagnosisProject(t, tc.image+diagnosisDigest)
			got := checkRegistriesWithMirrors(context.Background(), project, "prestart", transport, []string{"https://mirror.example"})
			if (len(got) != 0) != tc.wantFailure {
				t.Fatalf("%+v", got)
			}
			for _, d := range got {
				if d.Cause == ImageDenied || d.Code == "COMPOSE_IMAGE_DENIED" {
					t.Fatalf("probe claimed image denial: %+v", d)
				}
			}
			if tc.name == "unrelated-registry" && mirrorCalls.Load() != 0 {
				t.Fatal("Hub mirror used for another registry")
			}
		})
	}
}

type registryRoundTripper func(*http.Request) (*http.Response, error)

func (f registryRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRegistryMirrorConfigIsBoundedAndSanitized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	raw := `{"registry-mirrors":["https://mirror.example/","http://cache.example:5000","https://user:secret@bad.example","https://bad.example/path","https://bad.example?token=secret","file:///tmp/no","https://mirror.example"]}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	got := configuredRegistryMirrors([]string{path})
	want := []string{"https://mirror.example", "http://cache.example:5000"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%v", got)
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(path, link)
	if got := configuredRegistryMirrors([]string{link}); len(got) != 0 {
		t.Fatalf("followed config symlink: %v", got)
	}
	if err := os.WriteFile(path, []byte(`{"registry-mirrors":["https://one.example","https://two.example","https://three.example","https://four.example","https://five.example"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := configuredRegistryMirrors([]string{path}); len(got) != 4 {
		t.Fatalf("unbounded mirrors: %v", got)
	}
	if err := os.WriteFile(path, []byte(`not json`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := configuredRegistryMirrors([]string{path}); len(got) != 0 {
		t.Fatalf("invalid config: %v", got)
	}
}

func TestReachableMirrorCancelsBlockedUpstream(t *testing.T) {
	var calls atomic.Int32
	mirror := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.String() != "/v2/" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected mirror request: %s", r.URL)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mirror.Close()
	canceled := make(chan struct{})
	transport := registryRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "registry-1.docker.io" {
			<-r.Context().Done()
			close(canceled)
			return nil, r.Context().Err()
		}
		return mirror.Client().Transport.RoundTrip(r)
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got := checkRegistriesWithMirrors(ctx, diagnosisProject(t, "app"+diagnosisDigest), "preinst", transport, []string{mirror.URL})
	if len(got) != 0 || ctx.Err() != nil || calls.Load() != 1 {
		t.Fatalf("mirror did not bypass blocked upstream: %+v %v", got, ctx.Err())
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("upstream probe leaked")
	}
}
