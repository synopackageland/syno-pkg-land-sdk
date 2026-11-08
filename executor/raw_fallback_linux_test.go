//go:build linux

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type boundedRawDSM struct{ *rawWebDSM }

func (f boundedRawDSM) WebAPIRaw(_ context.Context, _, _, _ string, _ int, _ map[string]any, out io.Writer) error {
	f.calls++
	for body := f.stdout; len(body) > 0; {
		n := min(len(body), 16<<10)
		if _, err := out.Write(body[:n]); err != nil {
			return err
		}
		body = body[n:]
	}
	return nil
}

type failingMemoryStaging struct {
	dirStaging
	mode    string
	creates int
}

func (s *failingMemoryStaging) Available() (int64, error) {
	switch s.mode {
	case "unavailable":
		return 0, os.ErrNotExist
	case "full":
		return 0, nil
	}
	return 1 << 20, nil
}

func (s *failingMemoryStaging) Create() (*os.File, string, error) {
	s.creates++
	if s.mode == "create" {
		return nil, "", os.ErrPermission
	}
	f, name, err := s.dirStaging.Create()
	if err == nil && (s.mode == "write" || s.mode == "sync") {
		f.Close()
		// Admission succeeds, but the actual write or synchronization fails.
		device := "/dev/full"
		if s.mode == "sync" {
			device = "/dev/null"
		}
		f, err = os.OpenFile(device, os.O_WRONLY, 0)
	}
	return f, name, err
}

func TestRawReadSmallMemoryAndLargePersistent(t *testing.T) {
	for _, size := range []int{32 << 10, (32 << 10) + 1, 1 << 20, (1 << 20) + 1, 2 << 20} {
		for _, length := range []string{"unknown", "header", "expected"} {
			t.Run(fmt.Sprintf("%d/%s", size, length), func(t *testing.T) {
				body := bytes.Repeat([]byte{0, 255, 'x'}, (size+2)/3)[:size]
				e, f, id, persistent := rawSetup(t, 8<<20, nil)
				e.DSM = boundedRawDSM{f}
				memory := dirStaging{t.TempDir()}
				e.ReadStaging = memory
				head := "Content-Type: application/octet-stream\r\n"
				if length == "header" {
					head += fmt.Sprintf("Content-Length: %d\r\n", size)
				}
				f.stdout = append([]byte(head+"\r\n"), body...)
				p := map[string]any{"api": "SYNO.MailClient.Message", "method": "get_original", "version": 10, "params": map[string]any{"id": 42}, "response_type": "raw"}
				if length == "expected" {
					p["expected_size"] = size
				}
				r := webCall(e, id, true, p)
				if !r.OK || f.calls != 1 {
					t.Fatal("transfer failed or DSM was redispatched", r, f.calls)
				}
				reply := r.Result.(map[string]any)
				if size <= inlineWebAPIRaw {
					if !bytes.Equal(reply["body"].([]byte), body) || reply["staging"] != nil {
						t.Fatal("small output was not inline")
					}
				} else {
					dir := persistent
					if size <= 1<<20 {
						dir = memory.dir
						if reply["memory_staging"] != true {
							t.Fatal("bounded output did not use memory staging")
						}
					} else if reply["memory_staging"] != nil {
						t.Fatal("large output used memory staging")
					}
					got, err := os.ReadFile(filepath.Join(dir, reply["staging"].(string)))
					if err != nil || !bytes.Equal(got, body) {
						t.Fatal("staged bytes changed", err)
					}
				}
				if size <= 1<<20 {
					if entries, _ := os.ReadDir(persistent); len(entries) != 0 {
						t.Fatal("small read touched persistent staging")
					}
				} else if entries, _ := os.ReadDir(memory.dir); len(entries) != 0 {
					t.Fatal("large read left memory staging")
				}
				if e.rawReserved != 0 {
					t.Fatal("persistent reservation leaked")
				}
			})
		}
	}
}

func TestRawReadFallsBackWhenMemoryFails(t *testing.T) {
	for _, mode := range []string{"unavailable", "full", "create", "write", "sync"} {
		t.Run(mode, func(t *testing.T) {
			body := bytes.Repeat([]byte("original bytes"), 8192)
			e, f, id, persistent := rawSetup(t, 8<<20, body)
			e.DSM = boundedRawDSM{f}
			memory := &failingMemoryStaging{dirStaging: dirStaging{t.TempDir()}, mode: mode}
			e.ReadStaging = memory
			r := rawCall(e, id)
			if !r.OK || f.calls != 1 {
				t.Fatal("tmpfs failure rejected or redispatched transfer", r, f.calls)
			}
			reply := r.Result.(map[string]any)
			if reply["memory_staging"] != nil {
				t.Fatal("persistent fallback identified as tmpfs")
			}
			got, err := os.ReadFile(filepath.Join(persistent, reply["staging"].(string)))
			if err != nil || !bytes.Equal(got, body) {
				t.Fatal("fallback lost buffered bytes", err)
			}
			if entries, _ := os.ReadDir(memory.dir); len(entries) != 0 {
				t.Fatal("failed tmpfs attempt leaked a file")
			}
			if e.rawReserved != 0 {
				t.Fatal("fallback reservation leaked")
			}
			if mode != "unavailable" && mode != "full" && memory.creates != 1 {
				t.Fatal("test did not reach the failed handoff", memory.creates)
			}
		})
	}
}

func TestRawReadMemoryHandoffsStayBounded(t *testing.T) {
	e, f, id, persistent := rawSetup(t, 8<<20, bytes.Repeat([]byte("x"), 1<<20))
	e.DSM = boundedRawDSM{f}
	memory := dirStaging{t.TempDir()}
	e.ReadStaging = memory
	first, second := rawCall(e, id), rawCall(e, id)
	if !first.OK || !second.OK || first.Result.(map[string]any)["memory_staging"] != true ||
		second.Result.(map[string]any)["memory_staging"] != nil {
		t.Fatal("unconsumed memory output did not force persistent fallback", first, second)
	}
	used, err := memory.Used()
	if err != nil || used != 1<<20 {
		t.Fatal("memory staging exceeded aggregate cap", used, err)
	}
	if entries, _ := os.ReadDir(persistent); len(entries) != 1 {
		t.Fatal("second transfer did not use persistent staging")
	}
}

func TestRawReadSmallBodyDoesNotProbePersistentStaging(t *testing.T) {
	e, f, id, persistent := rawSetup(t, 8<<20, bytes.Repeat([]byte("x"), 64<<10))
	e.DSM = boundedRawDSM{f}
	e.ReadStaging = dirStaging{t.TempDir()}
	absent := filepath.Join(persistent, "must-stay-absent")
	e.RootStaging = dirStaging{absent}
	if r := rawCall(e, id); !r.OK || r.Result.(map[string]any)["memory_staging"] != true {
		t.Fatal("small memory output required a persistent spool", r)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("persistent spool was created", err)
	}
}

func TestRawReadConcurrentMemoryHandoffsStayBounded(t *testing.T) {
	memory := dirStaging{t.TempDir()}
	e := &Executor{ReadStaging: memory}
	var wg sync.WaitGroup
	body := bytes.Repeat([]byte("x"), 256<<10)
	names := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if name, ok := e.keepMemoryRaw(body); ok {
				names <- name
			}
		}()
	}
	wg.Wait()
	close(names)
	if len(names) != 4 {
		t.Fatal("concurrent handoffs violated aggregate cap", len(names))
	}
	for name := range names {
		got, err := os.ReadFile(filepath.Join(memory.dir, name))
		if err != nil || !bytes.Equal(got, body) {
			t.Fatal("concurrent handoff corrupted bytes", err)
		}
	}
}

func TestFileReadIgnoresRawMemoryCapacity(t *testing.T) {
	e, _, fr, id := readSetup(t)
	e.ReadStaging = &failingMemoryStaging{dirStaging: dirStaging{t.TempDir()}, mode: "full"}
	r := request(e, "files.read", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/docs/a.txt"})
	if !r.OK || fr.calls != 1 || r.Result.(map[string]any)["memory_staging"] != nil {
		t.Fatal("file read depended on optional raw tmpfs", r, fr.calls)
	}
}
