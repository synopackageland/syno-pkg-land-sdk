package delegate

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestClientTransmitsRemainingDeadlineWithReplyReserve(t *testing.T) {
	path := filepath.Join(t.TempDir(), "executor.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	seen := make(chan int64, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		raw, _ := bufio.NewReader(conn).ReadBytes('\n')
		var r Request
		json.Unmarshal(raw, &r)
		seen <- r.DeadlineMS
		conn.Write([]byte(`{"ok":true,"result":{}}` + "\n"))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := Client{Socket: path}
	if err := client.Call(ctx, "status", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := <-seen; n <= 0 || n >= 900 {
		t.Fatalf("deadline leaves no response reserve: %d", n)
	}
}
