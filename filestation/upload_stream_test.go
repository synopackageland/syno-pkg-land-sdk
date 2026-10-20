package filestation

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStreamingUploadWaitsForCommitResponseHeaders(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		time.Sleep(30 * time.Millisecond)
		fmt.Fprint(w, `{"success":true}`)
	})
	transport := c.http.Transport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = time.Millisecond
	c.http.Transport = transport
	if e := c.UploadStream(context.Background(), sessionRequest(), "/home", "fixture.bin", strings.NewReader("abc"), 3); e != nil {
		t.Fatal("metadata header deadline incorrectly limited upload", e)
	}
	if transport.ResponseHeaderTimeout != time.Millisecond {
		t.Fatal("upload mutated the shared metadata transport")
	}
}

func TestStreamingUploadHasExactContentLengthAndSession(t *testing.T) {
	payload := strings.Repeat("bounded-stream", 100000)
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength <= int64(len(payload)) || r.Header.Get("Cookie") != "id=test-session" {
			t.Error("length/session")
		}
		mr, e := r.MultipartReader()
		if e != nil {
			t.Fatal(e)
		}
		fields := map[string]string{}
		last := ""
		for {
			p, e := mr.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			b, _ := io.ReadAll(p)
			last = p.FormName()
			fields[last] = string(b)
		}
		if last != "file" || fields["file"] != payload || fields["path"] != "/home" || fields["SynoToken"] != "test-token" || fields["create_parents"] != "false" || fields["overwrite"] != "" {
			t.Error("unsafe upload")
		}
		fmt.Fprint(w, `{"success":true}`)
	})
	if e := c.UploadStream(context.Background(), sessionRequest(), "/home", "file.bin", strings.NewReader(payload), int64(len(payload))); e != nil {
		t.Fatal(e)
	}
	for _, size := range []int64{-1, MaxUploadBytes + 1} {
		if c.UploadStream(context.Background(), sessionRequest(), "/home", "file.bin", strings.NewReader("x"), size) == nil {
			t.Fatal("unbounded")
		}
	}
	if c.UploadStream(context.Background(), sessionRequest(), "/home", "../bad", strings.NewReader("x"), 1) == nil {
		t.Fatal("traversal")
	}
}

func TestStreamingUploadMalformedReplyRemainsUncertain(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); fmt.Fprint(w, "broken") })
	if e := c.UploadStream(context.Background(), sessionRequest(), "/home", "file.bin", strings.NewReader("abc"), 3); e == nil || !strings.Contains(e.Error(), "commit-uncertain") {
		t.Fatal(e)
	}
}
