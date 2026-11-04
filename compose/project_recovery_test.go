package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectRecoveryHintNamesTheOwnedProjectAndPreservesData(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, "compose.lock.json")
	valid := `{"schema":1,"package":"ExampleApp","project_name":"spl-exampleapp"}`
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	hint := ProjectRecoveryHint(project)
	for _, want := range []string{"If Container Manager", "synopkgland install ExampleApp --repair", "stopped project spl-exampleapp", "/var/packages/ExampleApp/target/compose", "preserve volumes, data files and images", "different owner or path"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("missing recovery action %q: %s", want, hint)
		}
	}
	for _, raw := range []string{`{}`, valid + `{}`, strings.Replace(valid, "ExampleApp", "../OtherApp", 1), strings.Replace(valid, "spl-exampleapp", "bad\\nproject", 1)} {
		os.WriteFile(path, []byte(raw), 0600)
		if ProjectRecoveryHint(project) != "" {
			t.Fatal("untrusted identity entered the recovery log")
		}
	}
	os.Remove(path)
	os.Symlink(filepath.Join(project, "missing"), path)
	if ProjectRecoveryHint(project) != "" {
		t.Fatal("symlink accepted")
	}
}
