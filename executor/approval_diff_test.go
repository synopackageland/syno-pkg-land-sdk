package main

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/taskscript"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApprovalComparisonUsesProtectedPolicyAndSeparatesTemplate(t *testing.T) {
	old := []byte(`{"v":1,"package":"Example","app":"SYNO.SDS.Example.Instance","operations":["status","root.run"],"root":{"cli":{"kind":"binary","digest":"` + digestA + `"}}}`)
	next := []byte(`{"v":1,"package":"Example","app":"SYNO.SDS.Example.Instance","operations":["status","root.run"],"root":{"cli":{"kind":"binary","digest":"` + strings.Repeat("b", 64) + `"}}}`)
	diff := compareApproval(old, next, digestA, strings.Repeat("b", 64), false)
	if diff.Permissions != "unchanged" || len(diff.RootPrograms) != 1 || diff.RootPrograms[0].Old != digestA || diff.TaskTemplateChanged {
		t.Fatalf("%+v", diff)
	}
	diff = compareApproval(old, []byte(`{"operations":["status","root.run","root.start"]}`), digestA, strings.Repeat("b", 64), true)
	if diff.Permissions != "expanded" || !diff.TaskTemplateChanged {
		t.Fatalf("%+v", diff)
	}
	var p map[string]any
	json.Unmarshal(next, &p)
	p["scope"] = "home"
	next, _ = json.Marshal(p)
	if compareApproval(old, next, digestA, strings.Repeat("b", 64), false).Permissions != "review-required" {
		t.Fatal("claimed no expansion for a changed scope")
	}
}
func TestApprovalReadbackPublicationIsAtomicAndRootOwned(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root filesystem fixture")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "approval.json")
	if err := writeApprovalReadback(path, os.Getgid(), approvalDifference{Permissions: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !json.Valid(b) {
		t.Fatal(err, string(b))
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0640 {
		t.Fatal(st.Mode())
	}
}

func TestApprovalReadbackValidatesCandidateAndRetainsProtectedBaseline(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root INFO fixture")
	}
	dir := t.TempDir()
	runtime := filepath.Join(dir, "runtime")
	target := filepath.Join(dir, "target", "executor")
	root := filepath.Join(dir, "target", "root")
	for _, d := range []string{runtime, target, root} {
		os.MkdirAll(d, 0700)
	}
	policy := func(bytes string) []byte {
		return []byte(`{"v":1,"package":"Example","app":"SYNO.SDS.Example.Instance","operations":["status","root.run"],"root":{"cli":"` + hashBytes([]byte(bytes)) + `"}}`)
	}
	old, next := policy("old program"), policy("new program")
	os.WriteFile(filepath.Join(runtime, "policy.json"), old, 0600)
	os.WriteFile(filepath.Join(target, "policy.json"), next, 0600)
	os.WriteFile(filepath.Join(root, "cli.sh"), []byte("new program"), 0600)
	os.WriteFile(filepath.Join(target, "executor"), []byte("candidate executor"), 0700)
	ed := hashBytes([]byte("candidate executor"))
	pd := hashBytes(next)
	tasks, _ := taskscript.Generate("Example", pd)
	setup := map[string]any{"package": "Example", "policyDigest": pd, "executorDigest": ed, "tasks": tasks}
	b, _ := json.Marshal(setup)
	os.WriteFile(filepath.Join(target, "setup.json"), b, 0600)
	info := filepath.Join(dir, "INFO")
	os.WriteFile(info, []byte(executorAnchorKey+`="`+ed+`"`), 0644)
	linked := filepath.Join(dir, "package-target")
	os.Symlink(filepath.Join(dir, "target"), linked)
	diff, err := approvalReadback(context.Background(), "Example", runtime, filepath.Join(linked, "executor"), info)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Permissions != "unchanged" || diff.OldPolicyDigest != hashBytes(old) || diff.NewPolicyDigest != pd || diff.RootPrograms[0].Old != hashBytes([]byte("old program")) {
		t.Fatalf("%+v", diff)
	}
	tasks[0].Script += "\n# task migration"
	setup["tasks"] = tasks
	b, _ = json.Marshal(setup)
	os.WriteFile(filepath.Join(target, "setup.json"), b, 0600)
	diff, err = approvalReadback(context.Background(), "Example", runtime, target, info)
	if err != nil || !diff.TaskTemplateChanged {
		t.Fatal(diff, err)
	}
	os.WriteFile(filepath.Join(root, "cli.sh"), []byte("tampered"), 0600)
	if _, err = approvalReadback(context.Background(), "Example", runtime, target, info); err == nil {
		t.Fatal("unverified root bytes accepted")
	}
	os.WriteFile(filepath.Join(root, "cli.sh"), []byte("new program"), 0600)
	os.WriteFile(filepath.Join(target, "executor"), []byte("tampered executor"), 0700)
	if _, err = approvalReadback(context.Background(), "Example", runtime, target, info); err == nil {
		t.Fatal("unanchored candidate accepted")
	}
}
