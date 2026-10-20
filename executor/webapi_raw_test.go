package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type rawWebDSM struct {
	*webDSM
	stdout []byte
}

type rawStreamCommand struct {
	cmdStub
	stdout []byte
	used   bool
}

func (c *rawStreamCommand) StreamBody(ctx context.Context, path string, args []string, out io.Writer) ([]byte, error) {
	c.path, c.args, c.used = path, args, true
	_, err := out.Write(c.stdout)
	return []byte(`{"success":false,"error":{"code":117}}`), err
}

func (c *rawStreamCommand) Stream(ctx context.Context, path string, args []string, out io.Writer) error {
	c.path, c.args, c.used = path, args, true
	_, err := out.Write(c.stdout)
	return err
}

func TestWebAPIRawAdapterStreamsScalarID(t *testing.T) {
	c := &rawStreamCommand{stdout: bytes.Repeat([]byte{255}, 2<<20)}
	d := &liveDSM{Command: c}
	var out bytes.Buffer
	err := d.WebAPIRaw(context.Background(), "alice", "SYNO.MailClient.Message", "get_original", 10, map[string]any{"id": json.Number("42")}, &out)
	head, body, sep := bytes.Cut(out.Bytes(), []byte("\r\n\r\n"))
	if err != nil || !c.used || !contains(c.args, "runner=alice") || !contains(c.args, "id=42") || !bytes.Equal(body, c.stdout) || !sep || string(head) != "Content-Type: message/rfc822" {
		t.Fatal("not streamed as fixed runner/scalar id", err, c.args)
	}
	if err := d.WebAPIRaw(context.Background(), "alice", "SYNO.MailClient.Message", "get_original", 10, map[string]any{"runner": "root"}, &out); err == nil {
		t.Fatal("accepted runner injection")
	}
}

type refuseOutput struct{}

func (refuseOutput) Write([]byte) (int, error) { return 0, errOutputLimit }

func TestWebAPIRawStreamCancelsOnLimitAndTimeout(t *testing.T) {
	started := time.Now()
	err := (processCommand{}).Stream(context.Background(), "/bin/sh", []string{"-c", "while :; do printf '0123456789'; done"}, refuseOutput{})
	if !errors.Is(err, errOutputLimit) || time.Since(started) > 3*time.Second {
		t.Fatalf("limit did not stop process: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = (processCommand{}).Stream(ctx, "/bin/sh", []string{"-c", "sleep 10"}, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 3*time.Second {
		t.Fatalf("timeout did not stop process: %v", err)
	}
}

func TestWebAPIRawBodyFDSeparatesControlAndStopsOnLimit(t *testing.T) {
	var body bytes.Buffer
	control, err := (processCommand{}).StreamBody(context.Background(), "/bin/sh", []string{"-c", `printf 'original MIME' >&3; printf '{"success":false,"error":{"code":117}}'`}, &body)
	if err != nil || body.String() != "original MIME" || !json.Valid(control) {
		t.Fatalf("separate FD: %q %q %v", body.String(), control, err)
	}
	started := time.Now()
	_, err = (processCommand{}).StreamBody(context.Background(), "/bin/sh", []string{"-c", `while :; do printf '0123456789' >&3; done`}, refuseOutput{})
	if !errors.Is(err, errOutputLimit) || time.Since(started) > 3*time.Second {
		t.Fatalf("body limit did not kill process: %v", err)
	}
}

func (f *rawWebDSM) WebAPIRaw(ctx context.Context, runner, api, method string, version int, params map[string]any, out io.Writer) error {
	f.calls++
	f.runner, f.api, f.method, f.version, f.params = runner, api, method, version, params
	// Exercise delimiters across writes, including binary MIME bytes.
	for _, b := range f.stdout {
		if _, err := out.Write([]byte{b}); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func rawSetup(t *testing.T, max int64, body []byte) (*Executor, *rawWebDSM, string, string) {
	t.Helper()
	e, w := webSetup(t)
	var profile WebAPIProfile
	raw, _ := json.Marshal(map[string]any{"purpose": "Read original mail", "raw_max_bytes": max, "apis": map[string]any{"SYNO.MailClient.Message": map[string]any{"get_original": []int{10}}}})
	if err := json.Unmarshal(raw, &profile); err != nil {
		t.Fatal(err)
	}
	e.Policy.WebAPI["calendar"] = profile
	f := &rawWebDSM{webDSM: w, stdout: append([]byte("Content-Type: message/rfc822\r\nSet-Cookie: secret\r\n\r\n"), body...)}
	e.DSM = f
	dir := t.TempDir()
	e.RootStaging = dirStaging{dir}
	return e, f, webGrant(t, e, true), dir
}

func rawCall(e *Executor, id string) Response {
	return webCall(e, id, true, map[string]any{"api": "SYNO.MailClient.Message", "method": "get_original", "version": 10, "params": map[string]any{"id": 42}, "response_type": "raw"})
}

func TestWebAPIRawInlineAndStagedBytes(t *testing.T) {
	for _, size := range []int{0, 128, 32 << 10, (32 << 10) + 1, 2 << 20} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			body := bytes.Repeat([]byte{0, 255, '\r', '\n'}, (size+3)/4)[:size]
			e, f, id, dir := rawSetup(t, 25<<20, body)
			r := rawCall(e, id)
			if !r.OK {
				t.Fatalf("raw call: %+v", r)
			}
			wire, _ := json.Marshal(r)
			if len(wire) > 48<<10 || bytes.Contains(wire, []byte("secret")) {
				t.Fatal("unsafe frame")
			}
			encoded, _ := json.Marshal(r.Result)
			var out struct {
				Body        string `json:"body"`
				Staging     string `json:"staging"`
				Size        int64  `json:"size"`
				SHA256      string `json:"sha256"`
				ContentType string `json:"content_type"`
			}
			json.Unmarshal(encoded, &out)
			var got []byte
			if size <= 32<<10 {
				got, _ = base64.StdEncoding.DecodeString(out.Body)
				if out.Staging != "" {
					t.Fatal("unexpected staging")
				}
			} else {
				if out.Body != "" || !strings.HasPrefix(out.Staging, "root-") {
					t.Fatal("bad staging handle")
				}
				got, _ = os.ReadFile(filepath.Join(dir, out.Staging))
				os.Remove(filepath.Join(dir, out.Staging))
			}
			hash := sha256.Sum256(body)
			if !bytes.Equal(got, body) || out.Size != int64(size) || out.SHA256 != hex.EncodeToString(hash[:]) || out.ContentType != "message/rfc822" || f.runner != "alice" || f.params["id"] != json.Number("42") {
				t.Fatalf("metadata/bytes/runner mismatch: %s", encoded)
			}
		})
	}
}

func TestWebAPIRawLimitIsExplicitAndLegacyDigestUnchanged(t *testing.T) {
	e, f, id, dir := rawSetup(t, 64<<10, bytes.Repeat([]byte("x"), (64<<10)+1))
	if r := rawCall(e, id); r.Code != "limit-exceeded" || f.calls != 1 {
		t.Fatalf("over-limit: %+v", r)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("partial MIME kept")
	}
	profile := WebAPIProfile{Purpose: "Legacy", APIs: map[string]map[string][]int{"SYNO.Cal.Event": {"list": {6}}}}
	b, _ := json.Marshal(profile)
	if strings.Contains(string(b), "raw_max_bytes") {
		t.Fatal("changed legacy digest")
	}
	for _, limit := range []int64{-1, (64 << 20) + 1} {
		profile.RawMaxBytes = limit
		e.Policy.WebAPI["calendar"] = profile
		if validWebAPIProfiles(e.Policy) {
			t.Fatal("invalid profile limit accepted")
		}
	}
}

func TestWebAPIRawRejectsUnsafeOrIncompleteResponse(t *testing.T) {
	for _, stdout := range []string{
		"Content-Type: text/plain\r\nContent-Type: message/rfc822\r\n\r\nx",
		"Status: 302 Found\nLocation: /private\n\nx",
		"X-Accel-Redirect: /etc/shadow\n\n",
		"X-Accel-Redirect: /etc/shadow\n\nx",
		"Transfer-Encoding: chunked\n\nx",
		"Content-Length: 4\n\nabc",
		"Content-Type: text/plain\n\n" + strings.Repeat("x", (64<<10)+1),
		"X: " + strings.Repeat("a", 8192) + "\n\nx",
		strings.Repeat("X: a\n", 33) + "\nx",
		"Bad Header\n\nx", "Content-Type: x\x00y\n\nx", "missing CGI headers",
	} {
		e, f, id, dir := rawSetup(t, 64<<10, nil)
		f.stdout = []byte(stdout)
		r := rawCall(e, id)
		if r.OK || r.Code == "INVALID_REQUEST" {
			t.Fatalf("expected explicit response refusal: %+v", r)
		}
		files, _ := os.ReadDir(dir)
		if len(files) != 0 {
			t.Fatal("failed response leaked staging")
		}
	}
}

func TestWebAPIRawRequiresProfileOptInAndPinsLimit(t *testing.T) {
	e, f, id, _ := rawSetup(t, 0, []byte("mail"))
	if r := rawCall(e, id); r.Code != "POLICY_DENIED" || f.calls != 0 {
		t.Fatalf("raw without opt-in: %+v", r)
	}
	e, f, id, _ = rawSetup(t, 25<<20, []byte("mail"))
	var changed WebAPIProfile
	b, _ := json.Marshal(e.Policy.WebAPI["calendar"])
	var fields map[string]any
	json.Unmarshal(b, &fields)
	fields["raw_max_bytes"] = float64(24 << 20)
	b, _ = json.Marshal(fields)
	json.Unmarshal(b, &changed)
	e.Policy.WebAPI["calendar"] = changed
	if r := rawCall(e, id); r.Code != "DELEGATION_DENIED" || f.calls != 0 {
		t.Fatalf("changed limit: %+v", r)
	}
}
