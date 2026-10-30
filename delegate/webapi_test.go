package delegate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
		var dst bytes.Buffer
		out, err := (JobContext{Client: c, DelegationID: "grant", StagingDir: dir}).WebAPI().CallRawTo(context.Background(), "SYNO.MailClient.Message", "get_original", 10, map[string]any{"id": 42}, &dst)
		if err != nil || !bytes.Equal(dst.Bytes(), body) || out.ContentType != "message/rfc822" || out.Size != int64(len(body)) {
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
	if _, err := (JobContext{Client: c, DelegationID: "grant", StagingDir: dir}).WebAPI().CallRawTo(context.Background(), "SYNO.MailClient.Message", "get_original", 10, nil, io.Discard); err == nil {
		t.Fatal("accepted corrupted MIME")
	}
	if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		t.Fatal("corrupt staging left behind")
	}
}

type zeroWebBody struct{}

func (zeroWebBody) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type boundedWebSink struct {
	n      int64
	max    int
	failAt int64
	err    error
	cancel context.CancelFunc
}

func (w *boundedWebSink) Write(p []byte) (int, error) {
	if w.cancel != nil {
		w.cancel()
	}
	if len(p) > w.max {
		return 0, errors.New("unbounded write")
	}
	if w.err != nil && w.n >= w.failAt {
		return 0, w.err
	}
	w.n += int64(len(p))
	return len(p), nil
}

func TestWebAPIRawToStreamsLargeBodyAndCleansUpOnFailure(t *testing.T) {
	const size = 65 << 20
	for _, mode := range []string{"complete", "writer disconnect", "canceled", "truncated", "symlink", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			os.Chmod(dir, 0700)
			name := "root-" + strings.Repeat("b", 32)
			path := filepath.Join(dir, name)
			f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0600)
			if err != nil {
				t.Fatal(err)
			}
			h := sha256.New()
			// Generate a sparse file and its digest with a fixed buffer.
			if err = f.Truncate(size); err != nil {
				t.Fatal(err)
			}
			if _, err = io.CopyN(h, zeroWebBody{}, size); err != nil {
				t.Fatal(err)
			}
			f.Close()
			wireSize := int64(size)
			switch mode {
			case "truncated":
				os.Truncate(path, size-1)
			case "symlink":
				os.Rename(path, filepath.Join(dir, "original"))
				os.Symlink("original", path)
			case "oversized":
				wireSize = MaxWebAPIRawBytes + 1
			}
			c := executor(t, func(q Request) string {
				var p map[string]any
				json.Unmarshal(q.Params, &p)
				if p["response_type"] != "raw" || p["api"] != "SYNO.SynologyDrive.Files" || p["version"] != float64(2) {
					t.Errorf("wrong download call: %s", q.Params)
				}
				b, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{
					"response_type": "raw", "content_type": "application/octet-stream",
					"size": wireSize, "sha256": hex.EncodeToString(h.Sum(nil)), "staging": name,
				}})
				return string(b) + "\n"
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			dst := &boundedWebSink{max: 32 << 10}
			if mode == "writer disconnect" {
				dst.err, dst.failAt = io.ErrClosedPipe, 1<<20
			}
			if mode == "canceled" {
				dst.cancel = cancel
			}
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			out, err := (JobContext{Client: c, DelegationID: "grant", StagingDir: dir}).WebAPI().CallRawTo(ctx, "SYNO.SynologyDrive.Files", "download", 2, map[string]any{"id": 42}, dst)
			runtime.ReadMemStats(&after)
			if mode == "complete" {
				if err != nil || dst.n != size || out.Size != size {
					t.Fatalf("large stream: %+v, bytes=%d err=%v", out, dst.n, err)
				}
				if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
					t.Fatalf("large body was buffered: allocated %d bytes", allocated)
				}
			} else if err == nil {
				t.Fatal("failed transfer accepted")
			}
			if mode == "writer disconnect" && !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("writer error lost: %v", err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("staging leaked")
			}
		})
	}
}

func TestWebAPIRawToRefusalAndNilWriter(t *testing.T) {
	calls := 0
	c := executor(t, func(Request) string {
		calls++
		return "{\"ok\":false,\"code\":\"DELEGATION_DENIED\"}\n"
	})
	w := (JobContext{Client: c, DelegationID: "grant"}).WebAPI()
	_, err := w.CallRawTo(context.Background(), "SYNO.SynologyDrive.Files", "download", 2, nil, nil)
	code(t, err, CodeInvalidRequest)
	dst := &boundedWebSink{max: 32 << 10}
	_, err = w.CallRawTo(context.Background(), "SYNO.SynologyDrive.Files", "download", 2, nil, dst)
	code(t, err, "DELEGATION_DENIED")
	if calls != 1 || dst.n != 0 {
		t.Fatal("refusal wrote bytes or retried")
	}
}

func TestWebAPIRawClientPropagatesLongAndShortBudgets(t *testing.T) {
	for _, timeout := range []time.Duration{time.Second, WebAPIRawTimeout} {
		c := executor(t, func(q Request) string {
			if q.DeadlineMS <= 0 || q.DeadlineMS > timeout.Milliseconds() {
				t.Errorf("deadline escaped parent: %d", q.DeadlineMS)
			}
			if timeout > time.Minute && q.DeadlineMS < (WebAPIRawTimeout-time.Minute).Milliseconds() {
				t.Errorf("large download kept old client cap: %d", q.DeadlineMS)
			}
			return "{\"ok\":false,\"code\":\"DELEGATION_DENIED\"}\n"
		})
		c.Timeout = ExecutorTimeout
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_, err := (JobContext{Client: c, DelegationID: "grant"}).WebAPI().CallRawTo(ctx, "SYNO.SynologyDrive.Files", "download", 2, nil, io.Discard)
		cancel()
		code(t, err, "DELEGATION_DENIED")
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
