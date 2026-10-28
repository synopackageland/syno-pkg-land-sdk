package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testHandle = "app-0123456789abcdef0123456789abcdef"

func uploadSetup(t *testing.T, max int64) (*Executor, *webDSM, string, string) {
	t.Helper()
	e, w := webSetup(t)
	p := e.Policy.WebAPI["calendar"]
	p.UploadMaxBytes = max
	p.APIs["SYNO.SynologyDrive.Files"] = map[string][]int{"create": {2}}
	e.Policy.WebAPI["calendar"] = p
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(parent, "executor-upload")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	e.Uploads = uploadDir{Dir: dir, UID: os.Getuid()}
	return e, w, webGrant(t, e, true), dir
}

func stage(t *testing.T, dir string, body []byte) map[string]any {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, testHandle), body, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(body)
	return map[string]any{"param": "file_content", "staging": testHandle, "size": len(body), "sha256": hex.EncodeToString(h[:])}
}

func uploadCall(e *Executor, id string, up map[string]any, params map[string]any) Response {
	if params == nil {
		params = map[string]any{"path": "/mydrive/a.xlsx"}
	}
	return webCall(e, id, true, map[string]any{"api": "SYNO.SynologyDrive.Files", "method": "create", "version": 2, "params": params, "upload": up})
}

func present(dir string) bool { _, err := os.Lstat(filepath.Join(dir, testHandle)); return err == nil }

func TestWebAPIUploadInjectsBase64AndUnlinks(t *testing.T) {
	for _, size := range []int{0, 1, 5000, 96000} {
		e, f, id, dir := uploadSetup(t, 96000)
		body := bytes.Repeat([]byte{0, 255, 7}, 32000)[:size]
		r := uploadCall(e, id, stage(t, dir, body), nil)
		if !r.OK || f.calls != 1 || f.params["file_content"] != base64.StdEncoding.EncodeToString(body) || f.params["path"] != "/mydrive/a.xlsx" {
			t.Fatalf("size %d: %+v %v", size, r, f.params)
		}
		if present(dir) {
			t.Fatal("upload not unlinked")
		}
		args, err := webAPIArgs("SYNO.SynologyDrive.Files", "create", "2", "alice", f.params)
		if err != nil {
			t.Fatalf("size %d argv: %v", size, err)
		}
		for _, a := range args {
			if len(a)+1 >= maxArgvString {
				t.Fatal("argument over MAX_ARG_STRLEN")
			}
		}
	}
}

func TestWebAPIUploadRefusals(t *testing.T) {
	body := []byte("PK\x03\x04 xlsx")
	cases := map[string]func(*Executor, map[string]any) (string, map[string]any){
		"size mismatch": func(e *Executor, up map[string]any) (string, map[string]any) {
			up["size"] = len(body) + 1
			return "INVALID_REQUEST", nil
		},
		"short size": func(e *Executor, up map[string]any) (string, map[string]any) {
			up["size"] = len(body) - 1
			return "INVALID_REQUEST", nil
		},
		"sha mismatch": func(e *Executor, up map[string]any) (string, map[string]any) {
			up["sha256"] = strings.Repeat("0", 64)
			return "INVALID_REQUEST", nil
		},
		"over declared limit": func(e *Executor, up map[string]any) (string, map[string]any) {
			up["size"] = 96001
			return "INVALID_REQUEST", nil
		},
		"over profile limit": func(e *Executor, up map[string]any) (string, map[string]any) {
			p := e.Policy.WebAPI["calendar"]
			p.UploadMaxBytes = 5
			e.Policy.WebAPI["calendar"] = p
			return "DELEGATION_DENIED", nil
		},
		"reserved param": func(e *Executor, up map[string]any) (string, map[string]any) {
			up["param"] = "sid"
			return "INVALID_REQUEST", nil
		},
		"bad param": func(e *Executor, up map[string]any) (string, map[string]any) {
			up["param"] = "a-b"
			return "INVALID_REQUEST", nil
		},
		"underscore": func(e *Executor, up map[string]any) (string, map[string]any) {
			up["param"] = "_x"
			return "INVALID_REQUEST", nil
		},
		"duplicate param": func(e *Executor, up map[string]any) (string, map[string]any) {
			return "INVALID_REQUEST", map[string]any{"file_content": "x"}
		},
		"traversal": func(e *Executor, up map[string]any) (string, map[string]any) {
			up["staging"] = "../app-0123456789abcdef0123456789abcdef"
			return "INVALID_REQUEST", nil
		},
		"wrong prefix": func(e *Executor, up map[string]any) (string, map[string]any) {
			up["staging"] = "root-0123456789abcdef0123456789abcdef"
			return "INVALID_REQUEST", nil
		},
		"missing size": func(e *Executor, up map[string]any) (string, map[string]any) {
			delete(up, "size")
			return "INVALID_REQUEST", nil
		},
		"unknown field": func(e *Executor, up map[string]any) (string, map[string]any) {
			up["path"] = "/etc/passwd"
			return "INVALID_REQUEST", nil
		},
		"no profile upload": func(e *Executor, up map[string]any) (string, map[string]any) {
			p := e.Policy.WebAPI["calendar"]
			p.UploadMaxBytes = 0
			e.Policy.WebAPI["calendar"] = p
			return "DELEGATION_DENIED", nil
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e, f, id, dir := uploadSetup(t, 96000)
			up := stage(t, dir, body)
			want, params := mutate(e, up)
			if name == "over profile limit" || name == "no profile upload" {
				// Profile digest changed after the grant: the grant itself is void.
				if r := uploadCall(e, id, up, params); r.OK || r.Code != want || f.calls != 0 {
					t.Fatalf("%+v", r)
				}
				return
			}
			if r := uploadCall(e, id, up, params); r.OK || r.Code != want || f.calls != 0 {
				t.Fatalf("%+v calls=%d", r, f.calls)
			}
			// A shape-valid refusal consumes the file; a malformed handle cannot name it.
			if name != "traversal" && name != "wrong prefix" && name != "missing size" && name != "unknown field" && present(dir) {
				t.Fatal("staged file survived refusal")
			}
		})
	}
}

func TestWebAPIUploadPolicyDeniedWithoutProfileLimit(t *testing.T) {
	// A grant made while the profile has a limit, then a profile that lacks it,
	// is DELEGATION_DENIED above; POLICY_DENIED is the direct policy check.
	e, f, _, dir := uploadSetup(t, 96000)
	p := e.Policy.WebAPI["calendar"]
	p.UploadMaxBytes = 0
	e.Policy.WebAPI["calendar"] = p
	id := webGrant(t, e, true)
	if r := uploadCall(e, id, stage(t, dir, []byte("x")), nil); r.Code != "POLICY_DENIED" || f.calls != 0 || present(dir) {
		t.Fatalf("%+v", r)
	}
}

func TestWebAPIUploadRefusedWithRawAndUnsupported(t *testing.T) {
	e, f, id, dir := uploadSetup(t, 96000)
	up := stage(t, dir, []byte("x"))
	r := webCall(e, id, true, map[string]any{"api": "SYNO.SynologyDrive.Files", "method": "create", "version": 2, "params": map[string]any{"a": 1}, "upload": up, "response_type": "raw"})
	if r.OK || r.Code != "INVALID_REQUEST" || f.calls != 0 {
		t.Fatalf("%+v", r)
	}
	if e.Policy.WebAPI["calendar"].allows("SYNO.SynologyDrive.Files", "create", 2) == false {
		t.Fatal("setup")
	}
	// The allowlist still applies: an unlisted method is refused before reading.
	r = webCall(e, id, true, map[string]any{"api": "SYNO.SynologyDrive.Files", "method": "delete", "version": 2, "params": map[string]any{"a": 1}, "upload": stage(t, dir, []byte("x"))})
	if r.Code != "POLICY_DENIED" || f.calls != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestWebAPIUploadParamsKeepCap(t *testing.T) {
	e, f, id, dir := uploadSetup(t, 96000)
	big := map[string]any{"x": strings.Repeat("a", maxWebAPIParams)}
	if r := uploadCall(e, id, stage(t, dir, []byte("x")), big); r.Code != "REQUEST_TOO_LARGE" || f.calls != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestWebAPIUploadFileChecks(t *testing.T) {
	body := []byte("hello")
	mk := func() (uploadDir, string, map[string]any) {
		_, _, _, dir := uploadSetup(t, 96000)
		return uploadDir{Dir: dir, UID: os.Getuid()}, dir, stage(t, dir, body)
	}
	sum := func(up map[string]any) string { return up["sha256"].(string) }
	t.Run("ok", func(t *testing.T) {
		u, dir, up := mk()
		got, err := u.Take(testHandle, 5, 96000, sum(up))
		if err != nil || string(got) != "hello" || present(dir) {
			t.Fatal(err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		u, dir, up := mk()
		target := filepath.Join(dir, "real")
		os.Rename(filepath.Join(dir, testHandle), target)
		os.Symlink(target, filepath.Join(dir, testHandle))
		if _, err := u.Take(testHandle, 5, 96000, sum(up)); err == nil || present(dir) {
			t.Fatal("followed symlink or kept link")
		}
	})
	t.Run("wrong owner", func(t *testing.T) {
		u, dir, up := mk()
		u.UID = os.Getuid() + 1
		if _, err := u.Take(testHandle, 5, 96000, sum(up)); err == nil {
			t.Fatal("accepted wrong owner")
		}
		_ = dir
	})
	t.Run("hardlink", func(t *testing.T) {
		u, dir, up := mk()
		other := filepath.Join(t.TempDir(), "x")
		if os.Link(filepath.Join(dir, testHandle), other) != nil {
			t.Skip("no hard links")
		}
		if _, err := u.Take(testHandle, 5, 96000, sum(up)); err == nil || present(dir) {
			t.Fatal("accepted hardlink or kept file")
		}
	})
	t.Run("group readable", func(t *testing.T) {
		u, dir, up := mk()
		os.Chmod(filepath.Join(dir, testHandle), 0640)
		if _, err := u.Take(testHandle, 5, 96000, sum(up)); err == nil || present(dir) {
			t.Fatal("accepted loose mode")
		}
	})
	t.Run("directory not private", func(t *testing.T) {
		u, dir, up := mk()
		os.Chmod(dir, 0755)
		if _, err := u.Take(testHandle, 5, 96000, sum(up)); err == nil {
			t.Fatal("accepted open directory")
		}
	})
	t.Run("directory symlink", func(t *testing.T) {
		u, dir, up := mk()
		link := filepath.Join(filepath.Dir(dir), "link")
		os.Symlink(dir, link)
		u.Dir = link
		if _, err := u.Take(testHandle, 5, 96000, sum(up)); err == nil {
			t.Fatal("followed directory symlink")
		}
	})
	t.Run("not regular", func(t *testing.T) {
		u, dir, up := mk()
		os.Remove(filepath.Join(dir, testHandle))
		os.Mkdir(filepath.Join(dir, testHandle), 0700)
		if _, err := u.Take(testHandle, 0, 96000, sum(up)); err == nil {
			t.Fatal("accepted directory")
		}
	})
	t.Run("file larger than limit", func(t *testing.T) {
		u, dir, _ := mk()
		os.WriteFile(filepath.Join(dir, testHandle), make([]byte, 100), 0600)
		if _, err := u.Take(testHandle, 100, 10, strings.Repeat("0", 64)); err == nil || present(dir) {
			t.Fatal("accepted oversize")
		}
	})
}

func TestWebAPIArgvLengthGuard(t *testing.T) {
	ok := strings.Repeat("a", int(maxWebAPIUploadBase64))
	if _, err := webAPIArgs("SYNO.X", "m", "1", "alice", map[string]any{strings.Repeat("p", 64): ok}); err != nil {
		t.Fatalf("maximum upload refused: %v", err)
	}
	for _, v := range []string{strings.Repeat("a", maxArgvString), strings.Repeat("a", maxArgvString-4)} {
		if _, err := webAPIArgs("SYNO.X", "m", "1", "alice", map[string]any{"p": v}); err == nil {
			t.Fatal("over-long argument not refused")
		}
	}
	if maxWebAPIUploadBase64 != 128000 || int(maxWebAPIUploadBase64)+2+len(strings.Repeat("p", 64))+1+1 >= maxArgvString {
		t.Fatal("limit arithmetic")
	}
}

func TestLiveDSMAcceptsOnlyTheInjectedUploadParam(t *testing.T) {
	c := &cmdStub{reply: `{"success":true}`}
	d := &liveDSM{Command: c}
	big := strings.Repeat("a", int(maxWebAPIUploadBase64))
	ctx := withUploadParam(context.Background(), "file_content")
	if _, err := d.WebAPI(ctx, "alice", "SYNO.SynologyDrive.Files", "create", 2, map[string]any{"file_content": big, "path": "/a"}); err != nil || !contains(c.args, "path=\"/a\"") {
		t.Fatalf("injected param refused: %v", err)
	}
	if _, err := d.WebAPI(context.Background(), "alice", "SYNO.SynologyDrive.Files", "create", 2, map[string]any{"file_content": big}); err == nil {
		t.Fatal("large param accepted without upload")
	}
	if _, err := d.WebAPI(ctx, "alice", "SYNO.SynologyDrive.Files", "create", 2, map[string]any{"file_content": big, "other": big}); err == nil {
		t.Fatal("second large param accepted")
	}
	if _, err := d.WebAPI(ctx, "alice", "SYNO.SynologyDrive.Files", "create", 2, map[string]any{"file_content": big + "a"}); err == nil {
		t.Fatal("over-limit upload accepted")
	}
	if _, err := d.WebAPI(withUploadParam(context.Background(), "sid"), "alice", "SYNO.SynologyDrive.Files", "create", 2, map[string]any{"sid": "x"}); err == nil {
		t.Fatal("reserved upload param accepted")
	}
}

func TestUploadMaxBytesProfileValidationAndDigest(t *testing.T) {
	e, _ := webSetup(t)
	before := profileDigest(e.Policy.WebAPI["calendar"])
	raw, _ := json.Marshal(e.Policy.WebAPI["calendar"])
	if strings.Contains(string(raw), "upload_max_bytes") {
		t.Fatal("omitted field serialized")
	}
	for max, ok := range map[int64]bool{0: true, 1: true, 96000: true, 96001: false, -1: false} {
		p := e.Policy
		p.WebAPI = map[string]WebAPIProfile{"calendar": e.Policy.WebAPI["calendar"]}
		prof := p.WebAPI["calendar"]
		prof.UploadMaxBytes = max
		p.WebAPI["calendar"] = prof
		if validWebAPIProfiles(p) != ok {
			t.Fatalf("upload_max_bytes %d valid=%v", max, !ok)
		}
		if max > 0 && profileDigest(prof) == before {
			t.Fatal("digest ignores upload_max_bytes")
		}
	}
	if profileDigest(e.Policy.WebAPI["calendar"]) != before {
		t.Fatal("digest changed")
	}
}
