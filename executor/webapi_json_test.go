package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

type jsonStreamCommand struct {
	pretty string
	chunk  int
}

func (jsonStreamCommand) Exec(context.Context, string, []string) ([]byte, error) {
	return nil, errors.New("JSON must bypass the generic command capture")
}
func (c jsonStreamCommand) Stream(_ context.Context, _ string, _ []string, w io.Writer) error {
	for start := 0; start < len(c.pretty); start += c.chunk {
		if _, err := io.WriteString(w, c.pretty[start:min(start+c.chunk, len(c.pretty))]); err != nil {
			return err
		}
	}
	return nil
}

func TestWebAPICompactStream(t *testing.T) {
	compact := `{"success":true,"data":{"number":9007199254740993,"string":"a b\n\"<>&","list":[1,2]}}`
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(compact), "", "    "); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range []int{1, 7, 32768} {
		d := &liveDSM{Command: jsonStreamCommand{pretty.String(), chunk}}
		got, err := d.WebAPI(context.Background(), "alice", "SYNO.Cal.Event", "list", 6, map[string]any{})
		if err != nil || string(got) != compact {
			t.Fatalf("chunk %d: %s %v", chunk, got, err)
		}
	}
	// Pretty stdout may exceed even the new cap: whitespace is never retained.
	w := new(webJSONOutput)
	padding := strings.Repeat(" ", 32768)
	for i := 0; i < maxWebAPIReply/len(padding)+1; i++ {
		if _, err := io.WriteString(w, padding); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := io.WriteString(w, compact); err != nil || string(w.buf) != compact {
		t.Fatal("pretty whitespace counted toward compact cap", err)
	}
	for _, invalid := range []string{`{"success":tru e}`, `{"success":true,"n":1 2}`} {
		w := new(webJSONOutput)
		if _, err := io.WriteString(w, invalid); err == nil && validateWebJSON(w.buf) == nil {
			t.Fatal("whitespace repaired invalid JSON")
		}
	}
}

func TestWebAPIJSONKeepsExtendedOperationBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	jsonCtx := context.WithValue(ctx, webJSONCommandKey{}, true)
	if got := streamCommandTimeout(jsonCtx); got < 14*time.Minute || got > 15*time.Minute {
		t.Fatalf("JSON streaming shortened the configured operation budget: %s", got)
	}
	if got := streamCommandTimeout(ctx); got > webAPIRawTimeout {
		t.Fatalf("raw command budget changed: %s", got)
	}
}

func TestWebAPIJSONValidationParity(t *testing.T) {
	for _, input := range []string{
		`{"success":false,"data":{"MixedCase":1}}`,
		`{"Success":true,"data":{"x":[{},[],null,-1.2e3]}}`,
		`{"success":true,"data":{"A":1,"a":2}}`,
		`{"success":true,"data":{"\u0061":1,"a":2}}`,
		`{"success":true,"data":{"\u212a":1,"k":2}}`,
		`{"success":true,"data":{"\ud83d\ude00":1,"😀":2}}`,
		`{"success":true,"data":{"\ud800":1,"\ufffd":2}}`,
		`{"success":true,"data":{"a\/b":1,"a/b":2}}`,
		`{"success":true,"data":{"number":1e10000}}`,
		`{"success":true,"success":false}`,
		`{"success":null}`, `{"success":1}`, `[]`, `{"data":{}}`,
	} {
		want := decodedWebJSONValid([]byte(input))
		if got := validateWebJSON([]byte(input)) == nil; got != want {
			t.Fatalf("%s: got %v, want %v", input, got, want)
		}
	}
}

// The independent standard-library oracle decodes tokens, retaining numbers
// as JSON numbers rather than imposing float64's range on DSM data.
func decodedWebJSONValid(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var envelope struct{ Success *bool }
	if uniqueValue(d, false, false) != nil {
		return false
	}
	_, err := d.Token()
	return err == io.EOF && json.Unmarshal(b, &envelope) == nil && envelope.Success != nil
}

// Measures the actual capture/validation/socket-write path with one maximum
// scalar. Input is streamed repeatedly from a fixed chunk, outside measurement.
func TestWebAPIJSONMemoryBound(t *testing.T) {
	chunk := []byte(strings.Repeat("<", 32<<10))
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	w := new(webJSONOutput)
	_, _ = io.WriteString(w, `{"success":true,"data":"`)
	left := maxWebAPIReply - len(w.buf) - 2
	for left > 0 {
		n := min(left, len(chunk))
		if _, err := w.Write(chunk[:n]); err != nil {
			t.Fatal(err)
		}
		left -= n
	}
	_, _ = io.WriteString(w, `"}`)
	if len(w.buf) != maxWebAPIReply || cap(w.buf) != maxWebAPIReply {
		t.Fatal(len(w.buf), cap(w.buf))
	}
	if err := validateWebJSON(w.buf); err != nil {
		t.Fatal(err)
	}
	if err := writeResponse(io.Discard, success(json.RawMessage(w.buf))); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > maxWebAPIReply+(1536<<10) {
		t.Fatalf("capture/validation/frame allocated %d bytes", allocated)
	}
	t.Logf("maximum scalar capture/validation/frame allocation: %d bytes", after.TotalAlloc-before.TotalAlloc)
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("accepted limit+1")
	} else {
		var size *webAPIReplyTooLarge
		if !errors.As(err, &size) || size.Size != maxWebAPIReply+1 {
			t.Fatal(err)
		}
	}
}

func TestWebAPIJSONSmallReplyMemory(t *testing.T) {
	chunk := []byte(strings.Repeat("x", 1024))
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	w := new(webJSONOutput)
	_, _ = io.WriteString(w, `{"success":true,"data":"`)
	for i := 0; i < 128; i++ {
		_, _ = w.Write(chunk)
	}
	_, _ = io.WriteString(w, `"}`)
	if err := validateWebJSON(w.buf); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 512<<10 {
		t.Fatalf("ordinary reply allocated the maximum payload buffer: %d bytes", allocated)
	}
}

func TestWebAPIJSONKeyIndexBound(t *testing.T) {
	const count = 50000
	b := []byte(`{"success":true,"data":[` + strings.Repeat(`{"":0},`, count-1) + `{"":0}]}`)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := validateWebJSON(b); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8*(count+2)+(128<<10) {
		t.Fatalf("key index allocation is not bounded: %d bytes", allocated)
	}
}

func FuzzWebAPIJSONValidator(f *testing.F) {
	for _, seed := range []string{`{"success":true}`, `{"success":false,"data":{"a":1,"A":2}}`, `{"success":true,"data":{"\u0061":0}}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 64<<10 {
			t.Skip()
		}
		var compact bytes.Buffer
		if json.Compact(&compact, []byte(input)) != nil {
			return
		}
		b := compact.Bytes()
		want := decodedWebJSONValid(b)
		if got := validateWebJSON(b) == nil; got != want {
			t.Fatalf("validation differs for %q: got %v want %v", b, got, want)
		}
	})
}

func TestWebAPIReplySocketEndToEnd(t *testing.T) {
	for _, n := range []int{1 << 20, maxWebAPIReply - len(`{"success":true,"data":""}`)} {
		e, f := webSetup(t)
		id := webGrant(t, e, true)
		f.reply = json.RawMessage(`{"success":true,"data":"` + strings.Repeat("<", n) + `"}`)
		server, client := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer server.Close()
			e.Connection(context.Background(), server, 1205, func(net.Conn) (int, error) { return 1205, nil })
		}()
		_, _ = io.WriteString(client, `{"v":1,"op":"webapi.call","params":{"delegation_id":"`+id+`","api":"SYNO.Cal.Event","method":"list","version":6,"params":{}}}`+"\n")
		got, err := io.ReadAll(client)
		client.Close()
		<-done
		if err != nil || len(got) != len(f.reply)+len("{\"ok\":true,\"result\":}\n") || !bytes.Contains(got, f.reply) {
			t.Fatalf("socket payload %d: received %d bytes: %v", n, len(got), err)
		}
	}
	e, f := webSetup(t)
	id := webGrant(t, e, true)
	f.err = &webAPIReplyTooLarge{maxWebAPIReply + 1}
	if r := webCall(e, id, true, nil); r.Code != "WEBAPI_REPLY_TOO_LARGE" || !strings.Contains(r.Message, "20971521") || !strings.Contains(r.Message, "20971520") {
		t.Fatal(r)
	}
}

type heldWebReply struct {
	net.Conn
	started chan struct{}
	allow   <-chan struct{}
	once    sync.Once
}

func (c *heldWebReply) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.started) })
	<-c.allow
	return c.Conn.Write(p)
}

func TestWebAPIReplySlotsIncludeSlowSocketDelivery(t *testing.T) {
	e, _ := webSetup(t)
	id := webGrant(t, e, true)
	allow := make(chan struct{})
	var clients []net.Conn
	var done []chan struct{}
	for i := 0; i < rootRunSlots; i++ {
		server, client := net.Pipe()
		conn := &heldWebReply{Conn: server, started: make(chan struct{}), allow: allow}
		ended := make(chan struct{})
		go func() {
			defer close(ended)
			defer server.Close()
			e.Connection(context.Background(), conn, 1205, func(net.Conn) (int, error) { return 1205, nil })
		}()
		_, _ = io.WriteString(client, `{"v":1,"op":"webapi.call","params":{"delegation_id":"`+id+`","api":"SYNO.Cal.Event","method":"list","version":6,"params":{}}}`+"\n")
		<-conn.started
		clients, done = append(clients, client), append(done, ended)
	}
	if r := webCall(e, id, true, nil); r.Code != "ROOT_BUSY" {
		t.Errorf("slow readers released their output slots: %+v", r)
	}
	close(allow)
	for i, client := range clients {
		_, _ = io.Copy(io.Discard, client)
		client.Close()
		<-done[i]
	}
	if r := webCall(e, id, true, nil); !r.OK {
		t.Fatal("output slots were not released", r)
	}
}
