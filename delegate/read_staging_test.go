package delegate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestReadMemoryStagingAvoidsPersistentDirectory(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprint(raw), func(t *testing.T) {
			sum := sha256.Sum256([]byte("abc"))
			name := "read-example"
			if raw {
				name = "root-" + hex.EncodeToString(sum[:16])
			}
			reply := fmt.Sprintf(`{"ok":true,"result":{"staging":%q,"size":3,"memory_staging":true}}`+"\n", name)
			if raw {
				reply = fmt.Sprintf(`{"ok":true,"result":{"response_type":"raw","content_type":"text/plain","staging":%q,"size":3,"sha256":%q,"memory_staging":true}}`+"\n", name, hex.EncodeToString(sum[:]))
			}
			client := executor(t, func(Request) string { return reply })
			dir := filepath.Join(filepath.Dir(client.Socket), "read-staging")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte("abc"), 0600); err != nil {
				t.Fatal(err)
			}
			persistent := filepath.Join(t.TempDir(), "must-stay-absent")
			job := JobContext{Client: client, DelegationID: "grant", JobID: "job", StagingDir: persistent}
			if raw {
				dst := &testReadWriter{}
				if _, err := job.WebAPI().CallRawTo(context.Background(), "SYNO.MailClient.Message", "get_original", 10, nil, dst); err != nil || string(dst.b) != "abc" {
					t.Fatal("raw memory reply", err)
				}
			} else if got, err := job.Files().Read(context.Background(), "/home/example", 3); err != nil || string(got) != "abc" {
				t.Fatal("file memory reply", err)
			}
			if _, err := os.Stat(persistent); !os.IsNotExist(err) {
				t.Fatal("read created persistent staging")
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatal("memory output was not consumed")
			}
		})
	}
}

type testReadWriter struct{ b []byte }

func (w *testReadWriter) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}
