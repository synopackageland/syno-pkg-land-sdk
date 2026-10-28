package delegate

import (
	"context"
	"io"
	"os"
	"runtime"
	"testing"
)

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestWriteAndDownloadLimitsAreSeparate(t *testing.T) {
	if MaxWriteBytes != 100<<20 || MaxDownloadBytes != 64<<20 {
		t.Fatal(MaxWriteBytes, MaxDownloadBytes)
	}
}

func TestWriteOverLimitIsRefusedBeforeReadingOrStaging(t *testing.T) {
	c := executor(t, func(Request) string { t.Error("oversize write reached executor"); return "" })
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	f := JobContext{Client: c, JobID: "j", DelegationID: "d", StagingDir: dir}.Files()
	code(t, f.Write(context.Background(), "/home", "big.xlsx", failReader{t}, MaxWriteBytes+1), CodeLimitExceeded)
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Fatal("staging leaked", files)
	}
	// Negative and bad names stay invalid requests, not limit errors.
	code(t, f.Write(context.Background(), "/home", "a", failReader{t}, -1), CodeInvalidRequest)
}

type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) { r.t.Error("source was read"); return 0, io.EOF }

// Sizes above the old download limit up to the write limit pass validation.
func TestWriteAcceptsSizesAboveTheDownloadLimit(t *testing.T) {
	c := executor(t, func(Request) string { t.Error("failed read reached executor"); return "" })
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	f := JobContext{Client: c, JobID: "j", DelegationID: "d", StagingDir: dir}.Files()
	for _, size := range []int64{MaxDownloadBytes + 1, MaxWriteBytes} {
		// brokenReader fails at the first read: proof the size check passed.
		code(t, f.Write(context.Background(), "/home", "f", brokenReader{}, size), "STAGING_UNAVAILABLE")
	}
	// Downloads are unchanged: Read still refuses more than MaxDownloadBytes.
	_, e := f.Read(context.Background(), "/home/a", MaxDownloadBytes+1)
	code(t, e, CodeInvalidRequest)
}

// A write of exactly MaxWriteBytes streams into staging with bounded memory.
func TestWriteAtTheLimitStreamsWithBoundedMemory(t *testing.T) {
	var staged int64
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	c := executor(t, func(Request) string {
		entries, _ := os.ReadDir(dir)
		if len(entries) == 1 {
			if info, e := entries[0].Info(); e == nil {
				staged = info.Size()
			}
		}
		return `{"ok":true,"result":{}}` + "\n"
	})
	f := JobContext{Client: c, JobID: "j", DelegationID: "d", StagingDir: dir}.Files()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if e := f.Write(context.Background(), "/home", "max.xlsx", io.LimitReader(zeroReader{}, MaxWriteBytes), MaxWriteBytes); e != nil {
		t.Fatal(e)
	}
	runtime.ReadMemStats(&after)
	if staged != MaxWriteBytes {
		t.Fatalf("executor saw %d staged bytes", staged)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 16<<20 {
		t.Fatalf("100 MiB write allocated %d bytes", alloc)
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Fatal("staging leaked", files)
	}
}
