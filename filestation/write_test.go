package filestation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCreateAndUploadAreBoundedAndUseSession(t *testing.T) {
	calls := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Cookie") != "id=test-session" {
			t.Error("session missing")
		}
		if calls == 1 {
			r.ParseForm()
			if r.Form.Get("api") != "SYNO.FileStation.CreateFolder" || r.Form.Get("folder_path") != `["/home"]` || r.Form.Get("name") != `["new"]` || r.Form.Get("force_parent") != "false" {
				t.Error("create fields")
			}
		} else {
			m, e := r.MultipartReader()
			if e != nil {
				t.Fatal(e)
			}
			fields := map[string]string{}
			last := ""
			for {
				p, e := m.NextPart()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				b, _ := io.ReadAll(p)
				last = p.FormName()
				fields[last] = string(b)
				if last == "file" && p.FileName() != "a.txt" {
					t.Error("filename")
				}
			}
			if fields["file"] != "abc" || fields["path"] != "/home" || fields["api"] != "SYNO.FileStation.Upload" || fields["SynoToken"] != "test-token" || fields["create_parents"] != "false" || fields["overwrite"] != "" || last != "file" {
				t.Error("multipart fields")
			}
		}
		fmt.Fprint(w, `{"success":true,"data":{}}`)
	})
	r := sessionRequest()
	ctx := context.Background()
	if e := c.Create(ctx, r, "/home", "new"); e != nil {
		t.Fatal(e)
	}
	if e := c.UploadStream(ctx, r, "/home", "a.txt", strings.NewReader("abc"), 3); e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"../evil", "/evil", "a/b", "a\\b", "a\r\nx", ".", "..", "a,b"} {
		if c.UploadStream(ctx, r, "/home", n, strings.NewReader("abc"), 3) == nil {
			t.Fatal(n)
		}
	}
	if c.UploadStream(ctx, r, "/home", "a", strings.NewReader("abc"), MaxUploadBytes+1) == nil {
		t.Fatal("oversized write")
	}
	if calls != 2 {
		t.Fatal("invalid input reached server")
	}
}

func TestMutationMalformedReplyIsCommitUncertain(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "broken response") })
	for _, e := range []error{c.Create(context.Background(), sessionRequest(), "/home", "x"), c.UploadStream(context.Background(), sessionRequest(), "/home", "x", strings.NewReader("abc"), 3)} {
		if e == nil || !strings.Contains(e.Error(), "commit-uncertain") {
			t.Fatal(e)
		}
	}
}

func TestBinaryUploadDownloadChecksum(t *testing.T) {
	payload := []byte{0, 1, 2, 255, 128, '\n', '"', '<', '>'}
	var stored []byte
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mr, e := r.MultipartReader()
			if e != nil {
				t.Fatal(e)
			}
			for {
				p, e := mr.NextPart()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				if p.FormName() == "file" {
					stored, e = io.ReadAll(p)
					if e != nil {
						t.Fatal(e)
					}
				}
			}
			fmt.Fprint(w, `{"success":true}`)
		} else {
			w.Header().Set("Content-Disposition", `attachment; filename="bytes"`)
			w.Write(stored)
		}
	})
	if e := c.UploadStream(context.Background(), sessionRequest(), "/home", "bytes", bytes.NewReader(payload), int64(len(payload))); e != nil {
		t.Fatal(e)
	}
	out, e := c.Read(context.Background(), sessionRequest(), "/home/bytes")
	if e != nil {
		t.Fatal(e)
	}
	if sha256.Sum256(out) != sha256.Sum256(payload) {
		t.Fatal("binary checksum mismatch")
	}
}
