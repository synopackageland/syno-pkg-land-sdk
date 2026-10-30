package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type generatedWebDSM struct {
	*webDSM
	stream func(context.Context, io.Writer) error
}

func (d *generatedWebDSM) WebAPIRaw(ctx context.Context, runner, api, method string, version int, params map[string]any, dst io.Writer) error {
	d.calls++
	d.runner, d.api, d.method, d.version, d.params = runner, api, method, version, params
	return d.stream(ctx, dst)
}

type zeroWebReader struct{}

func (zeroWebReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func streamSetup(t *testing.T, body func(context.Context, io.Writer) error) (*Executor, *generatedWebDSM, string, string) {
	t.Helper()
	e, base := webSetup(t)
	e.Policy.WebAPI["calendar"] = WebAPIProfile{Purpose: "Import Drive files", RawMaxBytes: maxWebAPIRaw,
		APIs: map[string]map[string][]int{"SYNO.SynologyDrive.Files": {"download": {2}}}}
	f := &generatedWebDSM{webDSM: base, stream: body}
	e.DSM = f
	dir := t.TempDir()
	e.RootStaging = dirStaging{dir}
	if !validWebAPIProfiles(e.Policy) {
		t.Fatal("2 GiB profile rejected")
	}
	return e, f, webGrant(t, e, true), dir
}

func streamCall(e *Executor, id string) Response {
	return webCall(e, id, true, map[string]any{"api": "SYNO.SynologyDrive.Files", "method": "download",
		"version": 2, "params": map[string]any{"id": 42}, "response_type": "raw"})
}

func TestWebAPIStreamsBeyond64MiBAsGrantOwner(t *testing.T) {
	const size = 65 << 20
	e, f, id, dir := streamSetup(t, func(ctx context.Context, dst io.Writer) error {
		until, ok := ctx.Deadline()
		if !ok || time.Until(until) < 29*time.Minute || streamCommandTimeout(ctx) < 29*time.Minute {
			return errors.New("old timeout still limits raw transfer")
		}
		if _, err := io.WriteString(dst, "Content-Type: application/octet-stream\r\nContent-Length: "+strconv.Itoa(size)+"\r\n\r\n"); err != nil {
			return err
		}
		_, err := io.CopyN(dst, zeroWebReader{}, size)
		return err
	})
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	r := streamCall(e, id)
	runtime.ReadMemStats(&after)
	if !r.OK {
		t.Fatalf("large download: %+v", r)
	}
	b, _ := json.Marshal(r.Result)
	var result struct {
		Staging string `json:"staging"`
		Size    int64  `json:"size"`
		SHA256  string `json:"sha256"`
	}
	json.Unmarshal(b, &result)
	staged, err := os.Open(filepath.Join(dir, result.Staging))
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	h := sha256.New()
	n, err := io.Copy(h, staged)
	if err != nil || n != size || result.Size != size || hex.EncodeToString(h.Sum(nil)) != result.SHA256 {
		t.Fatal("incomplete/corrupt staged stream", n, err)
	}
	if f.runner != "alice" || f.version != 2 || f.calls != 1 || len(b) > 1024 {
		t.Fatal("runner, pin or bounded reply violated")
	}
	if e.rawReserved != 0 {
		t.Fatal("quota reservation leaked")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("executor buffered large body: allocated %d bytes", allocated)
	}
}

func TestWebAPIRawDriveAdapterUsesPinnedRunnerAndStreamingCommand(t *testing.T) {
	c := &rawStreamCommand{stdout: []byte("Content-Type: application/octet-stream\n\nfile")}
	var dst strings.Builder
	if err := (&liveDSM{Command: c}).WebAPIRaw(context.Background(), "alice",
		"SYNO.SynologyDrive.Files", "download", 2, map[string]any{"id": json.Number("42")}, &dst); err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"runner=alice", "api=SYNO.SynologyDrive.Files", "method=download", "version=2", "id=42"} {
		if !contains(c.args, arg) {
			t.Fatalf("missing fixed argument %s: %v", arg, c.args)
		}
	}
	if !c.used || dst.String() != string(c.stdout) {
		t.Fatal("Drive response did not use streaming command")
	}
}

func TestWebAPIStreamFailuresRemovePartialStaging(t *testing.T) {
	for _, mode := range []string{"disconnect", "short body", "oversize header", "oversize body", "expired", "revoked", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			var e *Executor
			var id string
			e, _, id, _ = streamSetup(t, nil)
			f := e.DSM.(*generatedWebDSM)
			if mode == "disabled" {
				f.user.Admin = true
			}
			f.stream = func(ctx context.Context, dst io.Writer) error {
				length := int64(2 << 20)
				if mode == "oversize header" {
					length = maxWebAPIRaw + 1
				}
				if _, err := io.WriteString(dst, "Content-Type: application/octet-stream\nContent-Length: "+strconv.FormatInt(length, 10)+"\n\n"); err != nil {
					return err
				}
				if _, err := io.CopyN(dst, zeroWebReader{}, 1<<20); err != nil {
					return err
				}
				switch mode {
				case "disconnect":
					return io.ErrUnexpectedEOF
				case "oversize body":
					// Exercise the exact byte boundary without writing 2 GiB.
					dst.(*rawWebOutput).body.n = maxWebAPIRaw
					_, err := dst.Write([]byte{0})
					return err
				case "expired":
					e.commitMu.Lock()
					d, err := e.Store.Get(id)
					if err == nil {
						d.Lifetime = ""
						d.Expires = e.Now().Add(-time.Second)
						err = e.Store.Put(d)
					}
					e.commitMu.Unlock()
					return err
				case "revoked", "disabled":
					result := make(chan Response, 1)
					go func() {
						if mode == "revoked" {
							result <- request(e, "delegate.revoke", map[string]any{"delegation_id": id})
						} else {
							result <- request(e, "delegate.disable", map[string]any{})
						}
					}()
					select {
					case <-ctx.Done():
					case <-time.After(2 * time.Second):
						return errors.New("grant fence did not cancel download")
					}
					// Revoke acknowledges after this admitted call drains.
					t.Cleanup(func() {
						select {
						case r := <-result:
							if !r.OK {
								t.Errorf("grant fence did not drain: %+v", r)
							}
						case <-time.After(2 * time.Second):
							t.Error("grant fence remained blocked")
						}
					})
					return ctx.Err()
				}
				return nil
			}
			r := streamCall(e, id)
			want := "WEBAPI_UNCERTAIN"
			switch mode {
			case "oversize header", "oversize body":
				want = "limit-exceeded"
			case "expired", "revoked":
				want = "DELEGATION_DENIED"
			case "disabled":
				want = "disabled"
			}
			if r.OK || r.Code != want || f.calls != 1 {
				t.Fatalf("failure accepted or retried: %+v calls=%d", r, f.calls)
			}
			dir := e.RootStaging.(dirStaging).dir
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 0 || e.rawReserved != 0 {
				t.Fatal("partial output/reservation leaked", err)
			}
		})
	}
}

func TestWebAPIStreamRejectsInvalidGrantAndVersionBeforeDispatch(t *testing.T) {
	for _, mode := range []string{"revoked", "expired", "profile changed", "account disabled", "version changed", "wrong job"} {
		t.Run(mode, func(t *testing.T) {
			e, f, id, _ := streamSetup(t, func(context.Context, io.Writer) error {
				t.Error("invalid grant reached Drive")
				return nil
			})
			switch mode {
			case "revoked", "expired":
				d, _ := e.Store.Get(id)
				if mode == "revoked" {
					d.Revoked = true
				} else {
					d.Lifetime = ""
					d.Expires = e.Now().Add(-time.Second)
				}
				e.Store.Put(d)
			case "profile changed":
				p := e.Policy.WebAPI["calendar"]
				p.APIs = map[string]map[string][]int{"SYNO.SynologyDrive.Files": {"download": {3}}}
				e.Policy.WebAPI["calendar"] = p
			case "account disabled":
				f.user.Expired = "now"
			case "wrong job":
				d, _ := e.Store.Get(id)
				d.Job = "another-job"
				e.Store.Put(d)
			}
			var r Response
			if mode == "version changed" {
				r = webCall(e, id, true, map[string]any{"api": "SYNO.SynologyDrive.Files",
					"method": "download", "version": 3, "params": map[string]any{}, "response_type": "raw"})
			} else {
				r = streamCall(e, id)
			}
			if r.OK || f.calls != 0 {
				t.Fatalf("invalid grant admitted: %+v", r)
			}
		})
	}
}

func TestWebAPIStreamTimedGrantCancelsDuringTransfer(t *testing.T) {
	e, _, _, dir := streamSetup(t, func(ctx context.Context, dst io.Writer) error {
		if _, err := io.WriteString(dst, "Content-Type: application/octet-stream\n\n"); err != nil {
			return err
		}
		if _, err := io.CopyN(dst, zeroWebReader{}, 1<<20); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	})
	e.Now = time.Now
	id := webGrant(t, e, false)
	d, _ := e.Store.Get(id)
	d.Expires = e.Now().Add(50 * time.Millisecond)
	if err := e.Store.Put(d); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	r := webCall(e, id, false, map[string]any{"api": "SYNO.SynologyDrive.Files", "method": "download",
		"version": 2, "params": map[string]any{}, "response_type": "raw"})
	if r.Code != "DELEGATION_DENIED" || time.Since(start) > time.Second {
		t.Fatalf("expiry did not cancel transfer: %+v", r)
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Fatal("expired transfer kept partial output")
	}
}

func TestWebAPIStreamSocketDoesNotKeepOldOperationCap(t *testing.T) {
	for _, hint := range []int64{0, 250, 3600000} {
		t.Run(strconv.FormatInt(hint, 10), func(t *testing.T) {
			e, _, id, _ := streamSetup(t, func(ctx context.Context, dst io.Writer) error {
				deadline, ok := ctx.Deadline()
				left := time.Until(deadline)
				if !ok || hint == 250 && (left <= 0 || left > 250*time.Millisecond) ||
					hint != 250 && left < 29*time.Minute {
					return errors.New("socket kept old 30/60 second budget or escaped parent")
				}
				_, err := io.WriteString(dst, "Content-Type: application/octet-stream\n\nok")
				return err
			})
			server, client := net.Pipe()
			defer client.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer server.Close()
				e.Connection(context.Background(), server, os.Getuid(), func(net.Conn) (int, error) { return os.Getuid(), nil })
			}()
			client.SetDeadline(time.Now().Add(3 * time.Second))
			params, _ := json.Marshal(map[string]any{"delegation_id": id, "api": "SYNO.SynologyDrive.Files",
				"method": "download", "version": 2, "params": map[string]any{}, "response_type": "raw"})
			if err := json.NewEncoder(client).Encode(Request{Version: 1, Op: "webapi.call", Params: params, DeadlineMS: hint}); err != nil {
				t.Fatal(err)
			}
			var r Response
			if err := json.NewDecoder(client).Decode(&r); err != nil || !r.OK {
				t.Fatalf("socket raw call: %+v %v", r, err)
			}
			<-done
		})
	}
}

func TestWebAPIStreamQuotaAndExactBodyBoundary(t *testing.T) {
	e, f, id, _ := streamSetup(t, func(context.Context, io.Writer) error {
		t.Error("quota-refused download reached DSM")
		return nil
	})
	e.rawReserved = maxWebAPIRaw
	if r := streamCall(e, id); r.Code != "STORAGE_ERROR" || f.calls != 0 {
		t.Fatalf("unreserved large output admitted: %+v", r)
	}
	e.rawReserved = 0
	out := &rootOutput{limit: maxWebAPIRaw, n: maxWebAPIRaw - 1, failed: make(chan struct{})}
	if _, err := out.Write([]byte{0}); err != nil || out.n != maxWebAPIRaw {
		t.Fatal("exact 2 GiB boundary rejected", err)
	}
	if _, err := out.Write([]byte{0}); !errors.Is(err, errOutputLimit) {
		t.Fatal("body over 2 GiB accepted", err)
	}
}
