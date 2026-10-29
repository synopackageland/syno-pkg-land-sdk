package ipc

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNamedServersCloseHijackedConnections(t *testing.T) {
	run := filepath.Join(t.TempDir(), "run")
	for _, operation := range []string{"close", "shutdown", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hijacked := make(chan net.Conn, 1)
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: sample\r\n\r\n")
				rw.Flush()
				hijacked <- c
			})
			s, err := startUnix(ctx, run, "site.sock", h)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			other, err := startUnix(context.Background(), run, "other.sock", http.NotFoundHandler())
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			c, err := dialUnix(context.Background(), run, "site.sock")
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fmt.Fprint(c, "GET / HTTP/1.1\r\nHost: private\r\nConnection: Upgrade\r\nUpgrade: sample\r\n\r\n")
			c.SetReadDeadline(time.Now().Add(3 * time.Second))
			response, err := http.ReadResponse(bufio.NewReader(c), nil)
			if err != nil || response.StatusCode != 101 {
				t.Fatalf("upgrade: %v %v", response, err)
			}
			<-hijacked
			if operation == "shutdown" {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				err = s.Shutdown(ctx)
			} else if operation == "close" {
				err = s.Close()
			} else {
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Read(make([]byte, 1)); err == nil {
				t.Fatal("hijacked connection remained open")
			} else if e, ok := err.(net.Error); ok && e.Timeout() {
				t.Fatal("hijacked connection was not closed")
			}
			if err := s.Wait(); err != nil && !(operation == "cancel" && err == context.Canceled) {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(run, "site.sock")); !os.IsNotExist(err) {
				t.Fatalf("socket not cleaned up: %v", err)
			}
			if c, err := dialUnix(context.Background(), run, "other.sock"); err != nil {
				t.Fatal("stopping one server affected another:", err)
			} else {
				c.Close()
			}
		})
	}
}

func TestStopCancelsStreamingAndRetainsReplacementPath(t *testing.T) {
	run := filepath.Join(t.TempDir(), "run")
	canceled := make(chan struct{})
	s, err := startUnix(context.Background(), run, "stream.sock",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, "ready")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(canceled)
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialUnix(ctx, run, "stream.sock")
	}}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get("http://private/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if line, err := bufio.NewReader(response.Body).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("stream: %q %v", line, err)
	}
	path := filepath.Join(run, "stream.sock")
	os.Remove(path)
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("stream request was not canceled")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "replacement" {
		t.Fatalf("replacement removed: %q %v", data, err)
	}
}
