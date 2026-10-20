package sharedfolder

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNamesWithoutConfigIsEmpty(t *testing.T) {
	got, err := Names(t.TempDir())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestNamesReturnsNamesByKey(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"schema":1,"shared_folders":{"media":"Family Photos"}}`)
	got, err := Names(dir)
	if err != nil || got["media"] != "Family Photos" || len(got) != 1 {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestNamesRefusesUnknownSchemaAndBadNames(t *testing.T) {
	for _, body := range []string{
		`{"schema":2,"shared_folders":{"media":"x"}}`,
		`{"schema":1,"shared_folders":{"media":"a/b"}}`,
		`{"schema":1,"shared_folders":{"media":".."}}`,
		`{"schema":1,"shared_folders":{"media":"@appdata"}}`,
		`{"schema":1,"shared_folders":{"media":""}}`,
		`not json`,
	} {
		dir := t.TempDir()
		writeConfig(t, dir, body)
		if _, err := Names(dir); err == nil {
			t.Errorf("%s: want an error", body)
		}
	}
}

func TestResolveFindsTheNameOnItsVolume(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "volume2", "Media")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(root, "Media"); err != nil || got != want {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestResolveReportsMissingAndAmbiguous(t *testing.T) {
	root := t.TempDir()
	if _, err := Resolve(root, "Media"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	for _, v := range []string{"volume1", "volume2"} {
		if err := os.MkdirAll(filepath.Join(root, v, "Media"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Resolve(root, "Media"); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("ambiguous: %v", err)
	}
	if _, err := Resolve(root, "../x"); err == nil {
		t.Fatal("a name with .. must be refused")
	}
}

func TestAccessSatisfies(t *testing.T) {
	cases := []struct {
		have, need Access
		want       bool
	}{
		{None, Read, false}, {None, Write, false}, {Read, Read, true}, {Read, Write, false},
		{Write, Read, true}, {Write, Write, true}, {None, None, true},
	}
	for _, c := range cases {
		if got := c.have.Satisfies(c.need); got != c.want {
			t.Errorf("%q satisfies %q = %v, want %v", c.have, c.need, got, c.want)
		}
	}
}

// fixture builds <root>/volume1/<name> with the mode and stores the name
// under key "media".
func fixture(t *testing.T, mode os.FileMode) Config {
	t.Helper()
	root, data := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "volume1", "Media")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	writeConfig(t, data, `{"schema":1,"shared_folders":{"media":"Media"}}`)
	return Config{DataDir: data, Root: root, Account: testAccount(t), Required: map[string]Access{"media": Write}}
}

func skipRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file modes; the package account is never root")
	}
}

func TestStatusReadyWhenGrantedMeetsRequired(t *testing.T) {
	skipRoot(t)
	st, err := fixture(t, 0o755).Status()
	if err != nil || len(st.Folders) != 1 {
		t.Fatalf("%v %v", st, err)
	}
	f := st.Folders[0]
	if f.State != Ready || f.Granted != Write || f.Required != Write || f.Key != "media" || f.Name != "Media" || !f.Exists {
		t.Fatalf("%+v", f)
	}
	if st.Account != testAccount(t) || st.Pending() {
		t.Fatalf("%+v", st)
	}
}

func TestStatusPendingWhenReadOnlyButWriteRequired(t *testing.T) {
	skipRoot(t)
	st, _ := fixture(t, 0o555).Status()
	f := st.Folders[0]
	if f.State != Pending || f.Granted != Read || !st.Pending() {
		t.Fatalf("%+v", f)
	}
}

func TestStatusPendingWhenNoAccess(t *testing.T) {
	skipRoot(t)
	st, _ := fixture(t, 0o000).Status()
	f := st.Folders[0]
	if f.State != Pending || f.Granted != None {
		t.Fatalf("%+v", f)
	}
}

func TestStatusReadRequirementAcceptsReadOnly(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o555)
	cfg.Required = map[string]Access{"media": Read}
	st, _ := cfg.Status()
	if st.Folders[0].State != Ready || st.Pending() {
		t.Fatalf("%+v", st.Folders[0])
	}
}

func TestStatusWithoutDeclaredRequirementDefaultsToRead(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o555)
	cfg.Required = nil
	st, _ := cfg.Status()
	if st.Folders[0].Required != Read || st.Folders[0].State != Ready {
		t.Fatalf("%+v", st.Folders[0])
	}
}

func TestStatusMissingShareIsNotPending(t *testing.T) {
	cfg := fixture(t, 0o755)
	if err := os.RemoveAll(filepath.Join(cfg.Root, "volume1", "Media")); err != nil {
		t.Fatal(err)
	}
	st, _ := cfg.Status()
	f := st.Folders[0]
	if f.State != Missing || f.Exists || f.Path != "" || f.Error == "" || st.Pending() {
		t.Fatalf("%+v", f)
	}
}

func TestStatusAmbiguousShare(t *testing.T) {
	cfg := fixture(t, 0o755)
	if err := os.MkdirAll(filepath.Join(cfg.Root, "volume2", "Media"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, _ := cfg.Status()
	if st.Folders[0].State != Ambiguous {
		t.Fatalf("%+v", st.Folders[0])
	}
}

func TestStatusSortedByKeyAndEmptyWithoutConfig(t *testing.T) {
	data := t.TempDir()
	st, err := Config{DataDir: data, Root: t.TempDir()}.Status()
	if err != nil || len(st.Folders) != 0 {
		t.Fatalf("%v %v", st, err)
	}
	writeConfig(t, data, `{"schema":1,"shared_folders":{"b":"B","a":"A"}}`)
	st, _ = Config{DataDir: data, Root: t.TempDir()}.Status()
	if len(st.Folders) != 2 || st.Folders[0].Key != "a" || st.Folders[1].Key != "b" {
		t.Fatalf("%+v", st.Folders)
	}
}

func TestStatusCorruptConfigIsAnError(t *testing.T) {
	data := t.TempDir()
	writeConfig(t, data, `not json`)
	if _, err := (Config{DataDir: data}).Status(); err == nil {
		t.Fatal("want an error")
	}
}

func TestRequireReturnsPathWhenGranted(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o755)
	path, err := cfg.Require("media")
	if err != nil || path != filepath.Join(cfg.Root, "volume1", "Media") {
		t.Fatalf("%q %v", path, err)
	}
}

func TestRequireExplainsMissingPermission(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o555)
	_, err := cfg.Require("media")
	if !errors.Is(err, ErrPermissionPending) {
		t.Fatalf("got %v", err)
	}
	var perm *PermissionError
	if !errors.As(err, &perm) || perm.Account != testAccount(t) || perm.Need != Write || perm.Have != Read || perm.Name != "Media" {
		t.Fatalf("%+v", perm)
	}
	if perm.Error() == "" {
		t.Fatal("empty message")
	}
}

func TestRequireUnknownKeyAndMissingShare(t *testing.T) {
	cfg := fixture(t, 0o755)
	if _, err := cfg.Require("nope"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unknown key: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(cfg.Root, "volume1", "Media")); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Require("media"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing share: %v", err)
	}
}

func TestRequirePerRequirementOverride(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o555)
	if _, err := cfg.RequireAccess("media", Read); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := cfg.RequireAccess("media", Write); !errors.Is(err, ErrPermissionPending) {
		t.Fatalf("write: %v", err)
	}
}

func TestAPIErrorMapsToStableCodes(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{&PermissionError{Key: "media", Name: "Media", Account: "demo", Need: Write, Have: Read}, "shared_folder_permission"},
		{ErrNotConfigured, "shared_folder_not_configured"},
		{ErrNotFound, "shared_folder_not_found"},
		{ErrAmbiguous, "shared_folder_ambiguous"},
	}
	for _, c := range cases {
		var apiErr *api.Error
		if !errors.As(APIError(c.err), &apiErr) || apiErr.Code != c.code {
			t.Errorf("%v: got %v, want %s", c.err, APIError(c.err), c.code)
		}
	}
	other := errors.New("boom")
	if APIError(other) != other {
		t.Fatal("other errors pass through")
	}
	if APIError(nil) != nil {
		t.Fatal("nil stays nil")
	}
}

func testAccount(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	return u.Username
}
