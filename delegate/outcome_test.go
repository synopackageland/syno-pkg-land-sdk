package delegate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func checkNotSent(t *testing.T, err error, want bool) {
	t.Helper()
	if err == nil || NotSent(fmt.Errorf("app: %w", err)) != want {
		t.Fatalf("NotSent(%v) = %v, want %v", err, NotSent(err), want)
	}
	var public *api.Error
	if !errors.As(err, &public) {
		t.Fatalf("lost public code: %v", err)
	}
}

func TestNotSentLocalFailuresAndUnknownErrors(t *testing.T) {
	if NotSent(nil) || NotSent(errors.New("unknown")) || NotSent(&api.Error{Code: CodeAuthDenied}) {
		t.Fatal("unannotated errors cannot prove absence of execution")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []*Client{nil, {}, {Socket: filepath.Join(t.TempDir(), "missing")}} {
		checkNotSent(t, c.Call(context.Background(), "files.write", nil, nil, nil), true)
		checkNotSent(t, c.Call(ctx, "files.write", nil, nil, nil), true)
	}
	c := executor(t, func(Request) string { t.Error("invalid request sent"); return "" })
	checkNotSent(t, c.Call(context.Background(), "files.write", nil, make(chan int), nil), true)
	checkNotSent(t, c.Call(context.Background(), "files.write", nil, strings.Repeat("x", MaxFrame), nil), true)
	c.Mailbox = mailbox(t, "stop", 0)
	checkNotSent(t, c.Call(context.Background(), "files.write", nil, nil, nil), true)
	c.Socket = filepath.Join(t.TempDir(), "missing")
	c.Mailbox = mailbox(t, "start", 0)
	checkNotSent(t, c.Call(context.Background(), "status", nil, nil, nil), true)
	startup, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	checkNotSent(t, c.Call(startup, "files.write", nil, nil, nil), true)
}

func TestNotSentRefusalCatalog(t *testing.T) {
	raw, err := os.ReadFile("../spec/protocol/v1/errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Codes []struct {
			Code         string
			PreAdmission *bool
		}
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, entry := range catalog.Codes {
		t.Run(entry.Code, func(t *testing.T) {
			if entry.PreAdmission == nil {
				t.Fatal("missing preAdmission metadata")
			}
			c := executor(t, func(Request) string { return fmt.Sprintf("{\"ok\":false,\"code\":%q}\n", entry.Code) })
			err := c.Call(context.Background(), "files.write", nil, nil, nil)
			code(t, err, entry.Code)
			checkNotSent(t, err, *entry.PreAdmission)
		})
	}
}

func TestNotSentLostAndMalformedReplies(t *testing.T) {
	for _, reply := range []string{"", "{}\n", "\xff\n", "{\"ok\":true,\"result\":null}\n", "{\"ok\":true,\"result\":{\"n\":\"wrong\"}}\n", "{\"ok\":false,\"code\":\"bad code\"}\n", "{\"ok\":false,\"code\":\"FUTURE_CODE\"}\n", strings.Repeat("x", MaxFrame+1)} {
		c := executor(t, func(Request) string { return reply })
		var out struct{ N int }
		checkNotSent(t, c.Call(context.Background(), "files.write", nil, nil, &out), false)
	}
	entered, finish := make(chan struct{}), make(chan struct{})
	c := executor(t, func(Request) string { close(entered); <-finish; return "" })
	c.Timeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Call(ctx, "files.write", nil, nil, nil) }()
	<-entered
	cancel()
	checkNotSent(t, <-done, false)
	close(finish)
}

func TestNotSentQueueAndBusyCancellation(t *testing.T) {
	c := &Client{Socket: filepath.Join(t.TempDir(), "executor.sock")}
	for range executorRootSlots {
		release, err := acquireAdmission(context.Background(), c.Socket)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	checkNotSent(t, c.Call(ctx, "webapi.call", nil, nil, nil), true)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	cancel()
	checkNotSent(t, waitRootBusy(ctx, time.Minute), true)
}
