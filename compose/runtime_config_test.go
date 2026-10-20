//go:build linux

package compose

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRuntimeReplacementRejectsExpansionBeforeAllocation(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	os.WriteFile(source, []byte(strings.Repeat("x", 64<<10)), 0400)
	i := StorageInitialization{Directories: []WritableDirectory{{Base: base, Path: "run"}}, Runtime: &RuntimeConfiguration{Files: []RuntimeFile{{Target: filepath.Join(base, "run/config"), Source: source, Replace: []RuntimeReplacement{{Old: "x", New: strings.Repeat("y", 128)}}}}}}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := InitializeRuntime(context.Background(), i)
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("oversized expansion accepted")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 2<<20 {
		t.Fatalf("allocated %d bytes before rejecting bounded config", allocated)
	}
}

func TestRuntimeConfigPrivateLiteralAndSourceUnchanged(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "image.conf")
	os.WriteFile(source, []byte("Listen 80\nOther ${NATIVE}\n"), 0400)
	init := StorageInitialization{Directories: []WritableDirectory{{Base: base, Path: "runtime"}}, Runtime: &RuntimeConfiguration{
		Files:   []RuntimeFile{{Target: filepath.Join(base, "runtime/server.conf"), Source: source, Replace: []RuntimeReplacement{{Old: "Listen 80", New: "Listen ${LISTEN}"}}}},
		Prepare: []PrepareCommand{{Argv: []string{"/usr/bin/touch", filepath.Join(base, "runtime/prepared")}}},
	}}
	if err := InitializeRuntime(context.Background(), init); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(base, "runtime/server.conf"))
	st, _ := os.Stat(filepath.Join(base, "runtime/server.conf"))
	if string(b) != "Listen ${LISTEN}\nOther ${NATIVE}\n" || st.Mode().Perm() != 0600 {
		t.Fatalf("config: %q %v", b, st.Mode())
	}
	b, _ = os.ReadFile(source)
	if string(b) != "Listen 80\nOther ${NATIVE}\n" {
		t.Fatal("modified image asset")
	}
	if _, err := os.Stat(filepath.Join(base, "runtime/prepared")); err != nil {
		t.Fatal(err)
	}
	// Never follow a data symlink, even on restart with an existing directory.
	os.Remove(filepath.Join(base, "runtime/server.conf"))
	os.Symlink(source, filepath.Join(base, "runtime/server.conf"))
	if err := InitializeRuntime(context.Background(), init); err == nil {
		t.Fatal("symlink target accepted")
	}
}

func TestRuntimePrepareDeadlineExitAndNoSecretLogging(t *testing.T) {
	base := t.TempDir()
	i := StorageInitialization{Directories: []WritableDirectory{{Base: base, Path: "run"}}, Runtime: &RuntimeConfiguration{Prepare: []PrepareCommand{{Argv: []string{"/bin/sh", "-c", "echo secret-marker; exit 3"}, SuccessCodes: []int{0, 3}}}}}
	if err := InitializeRuntime(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	i.Runtime.Prepare[0].SuccessCodes = nil
	if err := InitializeRuntime(context.Background(), i); err == nil || strings.Contains(err.Error(), "secret-marker") {
		t.Fatalf("unsafe failure: %v", err)
	}
	i.Runtime.Prepare[0].Argv = []string{"/bin/sleep", "10"}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := InitializeRuntime(ctx, i); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("prepare not bounded: %v", err)
	}
}

func TestRuntimeDeclarationRejectsEscapesAndShellEntrypoint(t *testing.T) {
	for _, kind := range []string{"escape", "duplicate", "ambiguous", "missing-source", "missing-replacement", "relative-command"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			target := filepath.Join(base, "run/conf")
			i := StorageInitialization{Directories: []WritableDirectory{{Base: base, Path: "run"}}, Runtime: &RuntimeConfiguration{Files: []RuntimeFile{{Target: target, Content: "safe"}}}}
			switch kind {
			case "escape":
				i.Runtime.Files[0].Target = filepath.Join(base, "outside")
			case "duplicate":
				i.Runtime.Files = append(i.Runtime.Files, i.Runtime.Files[0])
			case "ambiguous":
				i.Runtime.Files[0].Source = "/etc/passwd"
			case "missing-source":
				i.Runtime.Files[0].Content = ""
				i.Runtime.Files[0].Source = filepath.Join(base, "absent")
			case "missing-replacement":
				i.Runtime.Files[0].Replace = []RuntimeReplacement{{Old: "not-present", New: "new"}}
			case "relative-command":
				i.Runtime.Prepare = []PrepareCommand{{Argv: []string{"sh", "-c", "true"}}}
			}
			if err := InitializeRuntime(context.Background(), i); err == nil {
				t.Fatal("unsafe runtime accepted")
			}
		})
	}
}
