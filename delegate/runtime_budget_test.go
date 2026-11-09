package delegate

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

func TestClientTransmitsRemainingDeadlineWithReplyReserve(t *testing.T) {
	for _, tc := range []struct {
		allowance time.Duration
		want      int64
	}{
		{125 * time.Second, 115000},
		{time.Second, 562},
		{5 * time.Second, 2812},
	} {
		t.Run(tc.allowance.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
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
				ctx, cancel := context.WithTimeout(context.Background(), tc.allowance)
				defer cancel()
				client := Client{Socket: path}
				if err := client.Call(ctx, "status", nil, nil, nil); err != nil {
					t.Fatal(err)
				}
				if n := <-seen; n != tc.want {
					t.Fatalf("deadline_ms = %d, want exactly %d (two distinct reply reserves)", n, tc.want)
				}
			})
		})
	}
}
