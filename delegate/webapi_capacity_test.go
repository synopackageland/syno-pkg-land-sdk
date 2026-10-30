package delegate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestRawMetadataBoundaryAndDestinationPreflight(t *testing.T) {
	for _, size := range []int64{-1, 0, 10 << 30, (10 << 30) + 1} {
		t.Run(strconv.FormatInt(size, 10), func(t *testing.T) {
			calls := 0
			c := executor(t, func(q Request) string {
				calls++
				var p struct {
					ExpectedSize int64 `json:"expected_size"`
				}
				if err := json.Unmarshal(q.Params, &p); err != nil || p.ExpectedSize != size {
					t.Errorf("metadata not carried: %s", q.Params)
				}
				return "{\"ok\":false,\"code\":\"DELEGATION_DENIED\"}\n"
			})
			_, err := (JobContext{Client: c, DelegationID: "grant"}).WebAPI().CallRawTo(
				context.Background(), "SYNO.SynologyDrive.Files", "download", 2, nil, io.Discard,
				RawWebAPIOptions{ExpectedSize: &size})
			if size < 0 || size > 10<<30 {
				code(t, err, CodeInvalidRequest)
				if calls != 0 {
					t.Fatal("invalid metadata dispatched")
				}
			} else {
				code(t, err, "DELEGATION_DENIED")
				if calls != 1 {
					t.Fatal("exact boundary was not admitted")
				}
			}
		})
	}
}

func TestRawDestinationCapacityBeforeDownloadAndCopy(t *testing.T) {
	for _, known := range []bool{true, false} {
		dir := t.TempDir()
		os.Chmod(dir, 0700)
		dst, err := os.CreateTemp(dir, "destination-")
		if err != nil {
			t.Fatal(err)
		}
		defer dst.Close()
		name := "root-" + strings.Repeat("a", 32)
		stage := dir + "/" + name
		if !known {
			if err := os.WriteFile(stage, []byte("abc"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		calls := 0
		c := executor(t, func(Request) string {
			calls++
			return `{"ok":true,"result":{"response_type":"raw","content_type":"application/octet-stream","size":3,"sha256":"` + strings.Repeat("0", 64) + `","staging":"` + name + `"}}` + "\n"
		})
		var expected *int64
		size := int64(3)
		if known {
			expected = &size
		}
		w := (JobContext{Client: c, DelegationID: "grant", StagingDir: dir}).WebAPI()
		_, err = w.callRawTo(context.Background(), "SYNO.SynologyDrive.Files", "download", 2, nil, dst,
			func(*os.File) (int64, error) { return 2, nil }, RawWebAPIOptions{ExpectedSize: expected})
		code(t, err, "NO_SPACE")
		wantCalls := 1
		if known {
			wantCalls = 0
		}
		info, _ := dst.Stat()
		if calls != wantCalls || info.Size() != 0 {
			t.Fatal("capacity failure dispatched or wrote destination", calls, info.Size())
		}
		if !known {
			if _, err := os.Stat(stage); !os.IsNotExist(err) {
				t.Fatal("capacity failure leaked staging")
			}
		}
		needed, err := w.rawDestinationNeed(dst, size)
		if err != nil || needed != 2*size {
			t.Fatal("same filesystem did not reserve both copies", needed, err)
		}
		if err := rawDestinationSpace(dst, needed, func(*os.File) (int64, error) { return needed, nil }); err != nil {
			t.Fatal("exact destination capacity rejected", err)
		}
		if err := rawDestinationSpace(dst, size, func(*os.File) (int64, error) { return 0, os.ErrPermission }); err == nil {
			t.Fatal("failed filesystem inspection admitted")
		}
	}
}

func TestRawClientTenGiBReplyBoundary(t *testing.T) {
	for _, size := range []int64{10 << 30, (10 << 30) + 1} {
		dir := t.TempDir()
		os.Chmod(dir, 0700)
		name := "root-" + strings.Repeat("b", 32)
		path := dir + "/" + name
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		// Sparse size metadata only: no multi-GiB writes, reads or hashes.
		if err := f.Truncate(10 << 30); err != nil {
			t.Fatal(err)
		}
		f.Close()
		c := executor(t, func(Request) string {
			b, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{
				"response_type": "raw", "content_type": "application/octet-stream",
				"size": size, "sha256": strings.Repeat("0", 64), "staging": name,
			}})
			return string(b) + "\n"
		})
		dst := &boundedWebSink{max: 32 << 10, err: io.ErrClosedPipe}
		_, err = (JobContext{Client: c, DelegationID: "grant", StagingDir: dir}).WebAPI().CallRawTo(
			context.Background(), "SYNO.SynologyDrive.Files", "download", 2, nil, dst)
		if size == 10<<30 {
			if !errors.Is(err, io.ErrClosedPipe) {
				t.Fatal("exact 10 GiB reply rejected before bounded copy", err)
			}
		} else {
			code(t, err, CodeExecutorProtocol)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("boundary check leaked staging")
		}
	}
}
