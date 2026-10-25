package main

// Approval explanations are diagnostics only. They never participate in task
// approval, pin matching, root installation, or executor handover.
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/taskscript"
)

type rootDifference struct {
	Name string `json:"name"`
	Old  any    `json:"old"`
	New  any    `json:"new"`
}
type policyDifference struct {
	Path string `json:"path"`
	Old  any    `json:"old"`
	New  any    `json:"new"`
}
type approvalDifference struct {
	Package             string             `json:"package"`
	OldPolicyDigest     string             `json:"old_policy_digest"`
	NewPolicyDigest     string             `json:"new_policy_digest"`
	NewExecutorDigest   string             `json:"new_executor_digest"`
	Permissions         string             `json:"permissions"`
	TaskTemplateChanged bool               `json:"task_template_changed"`
	RootPrograms        []rootDifference   `json:"root_programs"`
	PolicyChanges       []policyDifference `json:"policy_changes"`
}

// foldMemberNames renames members that encoding/json binds to a struct field
// by case folding ("Operations") to the field's canonical name, so the
// explanation reads the members the policy parser used. Other names are kept;
// strictJSON already refuses two members that fold to one name.
func foldMemberNames(m map[string]any, v any) {
	canonical := map[string]string{}
	t := reflect.TypeOf(v)
	for i := 0; i < t.NumField(); i++ {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			canonical[foldJSONKey(name)] = name
		}
	}
	for k, value := range m {
		if name, ok := canonical[foldJSONKey(k)]; ok && name != k {
			delete(m, k)
			m[name] = value
		}
	}
}

func policyView(b []byte) (map[string]any, map[string]any) {
	var p map[string]any
	json.Unmarshal(b, &p)
	foldMemberNames(p, Policy{})
	digests := map[string]any{}
	root, _ := p["root"].(map[string]any)
	for name, value := range root {
		if entry, ok := value.(map[string]any); ok {
			foldMemberNames(entry, RootEntry{})
			digests[name] = entry["digest"]
			delete(entry, "digest")
		} else {
			digests[name] = value
			root[name] = map[string]any{}
		}
	}
	return p, digests
}
func compareApproval(old, next []byte, oldDigest, newDigest string, template bool) approvalDifference {
	a, ar := policyView(old)
	b, br := policyView(next)
	out := approvalDifference{OldPolicyDigest: oldDigest, NewPolicyDigest: newDigest, Permissions: "unchanged", TaskTemplateChanged: template, RootPrograms: []rootDifference{}, PolicyChanges: []policyDifference{}}
	names := map[string]bool{}
	for k := range ar {
		names[k] = true
	}
	for k := range br {
		names[k] = true
	}
	keys := []string{}
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if !reflect.DeepEqual(ar[name], br[name]) {
			out.RootPrograms = append(out.RootPrograms, rootDifference{name, ar[name], br[name]})
		}
	}
	var walk func(any, any, string)
	walk = func(x, y any, path string) {
		if reflect.DeepEqual(x, y) {
			return
		}
		xm, xok := x.(map[string]any)
		ym, yok := y.(map[string]any)
		if xok && yok {
			all := map[string]bool{}
			for k := range xm {
				all[k] = true
			}
			for k := range ym {
				all[k] = true
			}
			ks := []string{}
			for k := range all {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			for _, k := range ks {
				walk(xm[k], ym[k], path+"/"+strings.NewReplacer("~", "~0", "/", "~1").Replace(k))
			}
			return
		}
		out.PolicyChanges = append(out.PolicyChanges, policyDifference{path, x, y})
	}
	walk(a, b, "")
	if len(out.PolicyChanges) > 0 {
		out.Permissions = "review-required"
	}
	ops := map[any]bool{}
	if values, ok := a["operations"].([]any); ok {
		for _, v := range values {
			ops[v] = true
		}
	}
	if values, ok := b["operations"].([]any); ok {
		for _, v := range values {
			if !ops[v] {
				out.Permissions = "expanded"
			}
		}
	}
	for name := range br {
		if _, ok := ar[name]; !ok {
			out.Permissions = "expanded"
		}
	}
	return out
}

// Called by the root monitor even while the operation socket is paused.
// The approved side comes only from the protected runtime. The candidate is
// explicitly unapproved, hashed here, and bound to the root-owned INFO anchor.
func approvalReadback(ctx context.Context, pkg, runtime, target, info string) (approvalDifference, error) {
	target, err := filepath.EvalSymlinks(target)
	if err != nil {
		return approvalDifference{}, err
	}
	old, _, err := readLifecycleFile(filepath.Join(runtime, "policy.json"), int(maxPolicyBytes))
	if err != nil {
		return approvalDifference{}, err
	}
	next, _, err := readLifecycleFile(filepath.Join(target, "policy.json"), int(maxPolicyBytes))
	if err != nil {
		return approvalDifference{}, err
	}
	candidate, err := parsePolicy(next, pkg)
	if err != nil {
		return approvalDifference{}, err
	}
	source, err := filepath.EvalSymlinks(filepath.Join(target, "../root"))
	if err != nil && len(candidate.Root) > 0 {
		return approvalDifference{}, err
	}
	for name, entry := range candidate.Root {
		program := name
		if entry.Kind != "binary" {
			program += ".sh"
		}
		digest, err := digestFile(ctx, filepath.Join(source, program), maxExecutorBytes)
		if err != nil || digest != entry.Digest {
			return approvalDifference{}, errors.New("candidate root digest")
		}
	}
	pd := hashBytes(next)
	ed, err := digestFile(ctx, filepath.Join(target, "executor"), maxExecutorBytes)
	if err != nil {
		return approvalDifference{}, err
	}
	anchor, err := executorAnchor(info, rootOwnedReadOnly)
	if err != nil || ed != anchor {
		return approvalDifference{}, errors.New("candidate anchor")
	}
	plan, _, err := readLifecycleFile(filepath.Join(target, "setup.json"), int(maxPolicyBytes))
	if err != nil {
		return approvalDifference{}, err
	}
	var setup struct {
		Package        string            `json:"package"`
		PolicyDigest   string            `json:"policyDigest"`
		ExecutorDigest string            `json:"executorDigest"`
		Tasks          []taskscript.Task `json:"tasks"`
	}
	if json.Unmarshal(plan, &setup) != nil || setup.Package != pkg || setup.PolicyDigest != pd || setup.ExecutorDigest != ed || len(setup.Tasks) != 2 {
		return approvalDifference{}, errors.New("candidate setup")
	}
	approved, err := taskscript.Generate(pkg, pd)
	if err != nil {
		return approvalDifference{}, err
	}
	template := false
	for i, t := range approved {
		n := setup.Tasks[i]
		if n.Kind != t.Kind || n.Name != t.Name || n.Header != t.Header {
			return approvalDifference{}, errors.New("candidate task identity")
		}
		if n.Script != t.Script {
			template = true
		}
	}
	out := compareApproval(old, next, hashBytes(old), pd, template)
	out.Package = pkg
	out.NewExecutorDigest = ed
	return out, nil
}
func writeApprovalReadback(path string, gid int, diff approvalDifference) error {
	b, err := json.Marshal(diff)
	if err != nil {
		return err
	}
	if len(b) > 128*1024 {
		return errors.New("approval explanation too large")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".approval-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Chown(0, gid)
	}
	if err == nil {
		err = f.Chmod(0640)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
