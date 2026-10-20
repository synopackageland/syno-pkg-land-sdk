package delegate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// The vectors are spec/protocol/v1/vectors, copied by go generate so the
// module's tests do not reach outside it.
func TestClientFramesProtocolVectors(t *testing.T) {
	files, err := filepath.Glob("testdata/vectors/*.jsonl")
	if err != nil || len(files) == 0 {
		t.Fatalf("no vectors: %v", err)
	}
	checked := 0
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
			var v struct {
				Name    string          `json:"name"`
				Request json.RawMessage `json:"request"`
				Expect  json.RawMessage `json:"expect"`
			}
			if err := json.Unmarshal(line, &v); err != nil {
				t.Fatal(err)
			}
			var req Request
			d := json.NewDecoder(bytes.NewReader(v.Request))
			d.DisallowUnknownFields()
			if d.Decode(&req) != nil || req.V != 1 {
				continue // Raw or malformed frames are not something the client sends.
			}
			t.Run(filepath.Base(file)+"/"+v.Name, func(t *testing.T) {
				sent := exchangeVector(t, req, v.Expect)
				var got, want any
				if err := json.Unmarshal(sent, &got); err != nil {
					t.Fatalf("frame %q: %v", sent, err)
				}
				json.Unmarshal(v.Request, &want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("client sent %s\nvector     %s", sent, v.Request)
				}
			})
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no vector exercised the client")
	}
}

// exchangeVector calls the client against a socket that answers expect and
// checks the refusal code the client reports. It returns the frame sent.
func exchangeVector(t *testing.T, req Request, expect json.RawMessage) []byte {
	t.Helper()
	dir, err := os.MkdirTemp("", "dv-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "executor.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	frames := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			frames <- nil
			return
		}
		defer c.Close()
		line, _ := bufio.NewReader(c).ReadBytes('\n')
		frames <- line
		c.Write(append(append([]byte{}, expect...), '\n'))
	}()
	var reply struct {
		OK     bool            `json:"ok"`
		Code   string          `json:"code"`
		Result json.RawMessage `json:"result"`
	}
	json.Unmarshal(expect, &reply)
	callErr := (&Client{Socket: socket}).Call(context.Background(), req.Op, req.Auth, req.Params, nil)
	line := <-frames
	if len(line) == 0 || line[len(line)-1] != '\n' || bytes.Count(line, []byte("\n")) != 1 {
		t.Fatalf("frame is not one line: %q", line)
	}
	var ae *api.Error
	switch {
	case !reply.OK:
		if !errors.As(callErr, &ae) || ae.Code != reply.Code {
			t.Fatalf("want refusal %s, got %v", reply.Code, callErr)
		}
	case bytes.HasPrefix(reply.Result, []byte("{")) && callErr != nil:
		t.Fatalf("accepted reply refused: %v", callErr)
	}
	return line
}
