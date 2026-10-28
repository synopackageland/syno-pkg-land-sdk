package delegate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebAPIRawClientConsumesAndVerifiesStaging(t *testing.T) {
	for _, inline := range []bool{true, false} {
		body := []byte("Subject: test\r\n\r\n\x00\xff")
		h := sha256.Sum256(body)
		dir := t.TempDir()
		os.Chmod(dir, 0700)
		name := "root-" + strings.Repeat("a", 32)
		if !inline {
			if err := os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
				t.Fatal(err)
			}
		}
		c := executor(t, func(q Request) string {
			var p map[string]any
			json.Unmarshal(q.Params, &p)
			if len(q.Auth) != 0 || p["response_type"] != "raw" || p["job_id"] != nil {
				t.Errorf("wrong raw request: %s", q.Params)
			}
			r := map[string]any{"response_type": "raw", "content_type": "message/rfc822", "sha256": hex.EncodeToString(h[:]), "size": len(body)}
			if inline {
				r["body"] = body
			} else {
				r["staging"] = name
			}
			b, _ := json.Marshal(map[string]any{"ok": true, "result": r})
			return string(b) + "\n"
		})
		out, err := (JobContext{Client: c, DelegationID: "grant", StagingDir: dir}).WebAPI().CallRaw(context.Background(), "SYNO.MailClient.Message", "get_original", 10, map[string]any{"id": 42})
		if err != nil || !bytes.Equal(out.Body, body) || out.ContentType != "message/rfc822" || out.Size != int64(len(body)) {
			t.Fatalf("raw client: %+v %v", out, err)
		}
		files, _ := os.ReadDir(dir)
		if len(files) != 0 {
			t.Fatal("staging was not consumed")
		}
	}
}

func TestWebAPIRawClientRejectsCorruptionAndRemovesStaging(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	name := "root-" + strings.Repeat("a", 32)
	os.WriteFile(filepath.Join(dir, name), []byte("abc"), 0600)
	c := executor(t, func(Request) string {
		return `{"ok":true,"result":{"response_type":"raw","content_type":"message/rfc822","size":3,"sha256":"` + strings.Repeat("0", 64) + `","staging":"` + name + `"}}` + "\n"
	})
	if _, err := (JobContext{Client: c, DelegationID: "grant", StagingDir: dir}).WebAPI().CallRaw(context.Background(), "SYNO.MailClient.Message", "get_original", 10, nil); err == nil {
		t.Fatal("accepted corrupted MIME")
	}
	if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		t.Fatal("corrupt staging left behind")
	}
}

func TestWebAPIClientCarriesGrantWithoutCredentials(t *testing.T) {
	c := executor(t, func(q Request) string {
		if q.Op != "webapi.call" || len(q.Auth) != 0 {
			t.Errorf("request %+v", q)
		}
		var p map[string]any
		json.Unmarshal(q.Params, &p)
		if p["delegation_id"] != "grant" || p["job_id"] != "job" || p["api"] != "SYNO.Cal.Event" || p["version"] != float64(6) {
			t.Errorf("params %v", p)
		}
		return "{\"ok\":true,\"result\":{\"success\":false,\"error\":{\"code\":9007}}}\n"
	})
	out, err := (JobContext{Client: c, JobID: "job", DelegationID: "grant"}).WebAPI().Call(context.Background(), "SYNO.Cal.Event", "list", 6, map[string]any{})
	if err != nil || string(out) != `{"success":false,"error":{"code":9007}}` {
		t.Fatalf("%s %v", out, err)
	}
}

func TestWebAPICallUploadStagesPrivatelyAndRemoves(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "executor-upload")
	body := []byte("PK\x03\x04\x00\xff xlsx")
	var staged string
	c := executor(t, func(q Request) string {
		var p struct {
			Upload struct {
				Param, Staging, SHA256 string
				Size                   int
			} `json:"upload"`
			Params map[string]any `json:"params"`
		}
		json.Unmarshal(q.Params, &p)
		sum := sha256.Sum256(body)
		path := filepath.Join(dir, p.Upload.Staging)
		info, err := os.Lstat(path)
		dinfo, _ := os.Lstat(dir)
		got, _ := os.ReadFile(path)
		if err != nil || info.Mode().Perm() != 0600 || dinfo.Mode().Perm() != 0700 || !bytes.Equal(got, body) ||
			p.Upload.Param != "file_content" || p.Upload.Size != len(body) || p.Upload.SHA256 != hex.EncodeToString(sum[:]) ||
			!strings.HasPrefix(p.Upload.Staging, "app-") || len(p.Upload.Staging) != 36 || p.Params["path"] != "/mydrive/a.xlsx" || p.Params["file_content"] != nil {
			t.Errorf("bad staged request: %s %v", q.Params, err)
		}
		staged = p.Upload.Staging
		return "{\"ok\":true,\"result\":{\"success\":true,\"data\":{}}}\n"
	})
	params := map[string]any{"path": "/mydrive/a.xlsx"}
	out, err := (JobContext{Client: c, DelegationID: "grant", UploadDir: dir}).WebAPI().CallUpload(context.Background(), "SYNO.SynologyDrive.Files", "create", 2, params, "file_content", body)
	if err != nil || string(out) != `{"success":true,"data":{}}` || staged == "" || len(params) != 1 {
		t.Fatalf("%s %v", out, err)
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Fatal("upload file left behind")
	}
}

func TestWebAPICallUploadRemovesFileOnFailureAndRefuses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "up")
	c := executor(t, func(Request) string { return "{\"ok\":false,\"code\":\"WEBAPI_UNCERTAIN\"}\n" })
	w := (JobContext{Client: c, DelegationID: "grant", UploadDir: dir}).WebAPI()
	if _, err := w.CallUpload(context.Background(), "SYNO.SynologyDrive.Files", "create", 2, nil, "file_content", []byte("x")); err == nil {
		t.Fatal("executor failure not reported")
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Fatal("upload file left behind after failure")
	}
	for name, run := range map[string]func() error{
		"oversize": func() error {
			_, e := w.CallUpload(context.Background(), "A", "m", 1, nil, "p", make([]byte, MaxWebAPIUploadBytes+1))
			return e
		},
		"duplicate": func() error {
			_, e := w.CallUpload(context.Background(), "A", "m", 1, map[string]any{"p": 1}, "p", nil)
			return e
		},
		"no param": func() error { _, e := w.CallUpload(context.Background(), "A", "m", 1, nil, "", nil); return e },
		"no dir": func() error {
			_, e := (JobContext{Client: c, DelegationID: "g"}).WebAPI().CallUpload(context.Background(), "A", "m", 1, nil, "p", nil)
			return e
		},
	} {
		if run() == nil {
			t.Fatal(name, "accepted")
		}
	}
	loose := t.TempDir()
	os.Chmod(loose, 0755)
	if _, err := (JobContext{Client: c, DelegationID: "g", UploadDir: loose}).WebAPI().CallUpload(context.Background(), "A", "m", 1, nil, "p", []byte("x")); err == nil {
		t.Fatal("accepted a directory others can read")
	}
	if files, _ := os.ReadDir(loose); len(files) != 0 {
		t.Fatal("wrote into a loose directory")
	}
}
