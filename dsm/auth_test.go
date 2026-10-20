package dsm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type fakeRunner struct {
	out  string
	err  error
	env  []string
	path string
}

func (f *fakeRunner) run(_ context.Context, path string, env []string) ([]byte, error) {
	f.path, f.env = path, env
	return []byte(f.out), f.err
}

func newTestAuth(t *testing.T, runner *fakeRunner) *Authenticator {
	t.Helper()
	a := NewAuthenticator()
	a.isAdministrator = func(_ context.Context, name string) (bool, error) {
		return name == "alice" || name == `CORP\alice` || name == "alice@example.com", nil
	}
	a.run = runner.run
	return a
}

func sessionRequest() *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/webman/3rdparty/App/cgi/api.cgi", nil)
	r.RemoteAddr = "192.0.2.7:51234"
	r.Header.Set("Cookie", "id=abc; did=xyz")
	r.Header.Set("X-SYNO-TOKEN", "tok+en/=")
	r.Header.Set("User-Agent", "should-not-pass")
	return r
}

func TestAdminUserIsResolvedFromAuthenticateCGIAndNativeProvider(t *testing.T) {
	runner := &fakeRunner{out: "alice\n"}
	id, err := newTestAuth(t, runner).Authenticate(sessionRequest())
	if err != nil || id.User != "alice" || !id.Admin {
		t.Fatalf("got %+v, %v", id, err)
	}
	if runner.path != AuthenticateCGI {
		t.Errorf("ran %q", runner.path)
	}
}

func TestOrdinaryUserIsNotAdmin(t *testing.T) {
	id, err := newTestAuth(t, &fakeRunner{out: "bob\n"}).Authenticate(sessionRequest())
	if err != nil || id.User != "bob" || id.Admin {
		t.Fatalf("got %+v, %v", id, err)
	}
}

func TestOnlySessionCarriersReachAuthenticateCGI(t *testing.T) {
	runner := &fakeRunner{out: "alice"}
	if _, err := newTestAuth(t, runner).Authenticate(sessionRequest()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"HTTP_COOKIE=id=abc; did=xyz",
		"HTTP_X_SYNO_TOKEN=tok+en/=",
		"QUERY_STRING=SynoToken=tok%2Ben%2F%3D",
		"REMOTE_ADDR=192.0.2.7",
		"REQUEST_METHOD=GET",
	}
	got := slices.Clone(runner.env)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("env = %q\nwant  %q", got, want)
	}
}

func TestMissingCookieNeverRunsAuthenticateCGI(t *testing.T) {
	runner := &fakeRunner{out: "alice"}
	r := sessionRequest()
	r.Header.Del("Cookie")
	_, err := newTestAuth(t, runner).Authenticate(r)
	if !errors.Is(err, api.ErrNoSession) || runner.path != "" {
		t.Fatalf("err=%v ran=%q", err, runner.path)
	}
}

func TestRejectedOrMalformedSessionsAreNoSession(t *testing.T) {
	for name, runner := range map[string]*fakeRunner{
		"exit status":     {err: &exitError{}},
		"empty output":    {out: "\n"},
		"two lines":       {out: "alice\nadmin\n"},
		"html output":     {out: "<html>login</html>"},
		"overlong name":   {out: strings.Repeat("a", 1025)},
		"control in name": {out: "alice\x00admin"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newTestAuth(t, runner).Authenticate(sessionRequest())
			if !errors.Is(err, api.ErrNoSession) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestBrokenAuthenticatorIsUnavailableNotNoSession(t *testing.T) {
	_, err := newTestAuth(t, &fakeRunner{err: errors.New("exec: not found")}).Authenticate(sessionRequest())
	if err == nil || errors.Is(err, api.ErrNoSession) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnavailableNativeRoleFailsClosed(t *testing.T) {
	a := newTestAuth(t, &fakeRunner{out: "alice"})
	a.isAdministrator = func(context.Context, string) (bool, error) { return false, errors.New("native unavailable") }
	if _, err := a.Authenticate(sessionRequest()); err == nil || errors.Is(err, api.ErrNoSession) {
		t.Fatalf("err=%v, want unavailable", err)
	}
}

type exitError struct{}

func (*exitError) Error() string { return "exit status 1" }
func (*exitError) ExitCode() int { return 1 }

func TestAdministratorMembershipIsQueriedEachRequestAndNotAnAccountName(t *testing.T) {
	runner := &fakeRunner{out: "admin"}
	a := newTestAuth(t, runner)
	currentAdmin := false
	calls := 0
	a.isAdministrator = func(context.Context, string) (bool, error) { calls++; return currentAdmin, nil }
	id, err := a.Authenticate(sessionRequest())
	if err != nil || id.Admin {
		t.Fatalf("account name granted privileges: %+v %v", id, err)
	}
	runner.out, currentAdmin = "alice", true
	id, err = a.Authenticate(sessionRequest())
	if err != nil || !id.Admin {
		t.Fatalf("native administrator refused: %+v %v", id, err)
	}
	currentAdmin = false
	id, err = a.Authenticate(sessionRequest())
	if err != nil || id.Admin || calls != 3 {
		t.Fatalf("stale role retained: %+v %v calls=%d", id, err, calls)
	}
}

func TestDirectoryAdministratorMustUseDSMRoleProvider(t *testing.T) {
	a := newTestAuth(t, &fakeRunner{out: `CORP\alice`})
	id, err := a.Authenticate(sessionRequest())
	if err != nil || !id.Admin {
		t.Fatalf("DSM directory administrator refused: %+v %v", id, err)
	}
}
