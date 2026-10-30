package main

import (
	"context"
	"io"
	"os"
	"strconv"
	"testing"
	"time"
)

type capacityStaging struct {
	dirStaging
	free, used int64
	err        error
}

func (s capacityStaging) Available() (int64, error) { return s.free, s.err }
func (s capacityStaging) Used() (int64, error) {
	n, err := s.dirStaging.Used()
	return n + s.used, err
}

func TestRawReservesActualSizeBeforeBody(t *testing.T) {
	for _, mode := range []string{"content length", "metadata", "unknown", "no space", "disk query failed", "quota", "reserved by other"} {
		t.Run(mode, func(t *testing.T) {
			const size int64 = 64 << 10
			var e *Executor
			e, f, id, dir := streamSetup(t, func(_ context.Context, dst io.Writer) error {
				head := "Content-Type: application/octet-stream\n"
				if mode != "unknown" && mode != "metadata" {
					head += "Content-Length: " + strconv.FormatInt(size, 10) + "\n"
				}
				if _, err := io.WriteString(dst, head+"\n"); err != nil {
					return err
				}
				if e.rawReserved != size || dst.(*rawWebOutput).body.limit != size {
					t.Errorf("reserved profile ceiling instead of actual size: %d", e.rawReserved)
				}
				_, err := io.CopyN(dst, zeroWebReader{}, size)
				return err
			})
			staging := capacityStaging{dirStaging: dirStaging{dir}, free: size}
			switch mode {
			case "content length":
				// A small download fits even with 9 GiB of previous staging.
				staging.used = 9 << 30
			case "no space":
				staging.free--
			case "disk query failed":
				staging.err = os.ErrPermission
			case "quota":
				staging.free = maxWebAPIStaging
				staging.used = maxWebAPIStaging - size + 1
			case "reserved by other":
				e.rawReserved = 1
			}
			e.RootStaging = staging
			var r Response
			if mode == "metadata" || mode == "no space" {
				r = webCall(e, id, true, map[string]any{"api": "SYNO.SynologyDrive.Files", "method": "download",
					"version": 2, "params": map[string]any{"id": 42}, "response_type": "raw", "expected_size": size})
			} else {
				r = streamCall(e, id)
			}
			want := ""
			switch mode {
			case "unknown", "no space", "reserved by other":
				want = "NO_SPACE"
			case "disk query failed", "quota":
				want = "STORAGE_ERROR"
			}
			if want == "" {
				if !r.OK {
					t.Fatalf("small download refused: %+v", r)
				}
			} else if r.OK || r.Code != want {
				t.Fatalf("wrong refusal: %+v, want %s", r, want)
			}
			if mode == "no space" && f.calls != 0 {
				t.Fatal("known insufficient space reached DSM")
			}
			left := int64(0)
			if mode == "reserved by other" {
				left = 1
			}
			if e.rawReserved != left {
				t.Fatal("reservation leaked", e.rawReserved)
			}
			files, _ := os.ReadDir(dir)
			if want != "" && len(files) != 0 {
				t.Fatal("capacity refusal left partial staging")
			}
		})
	}
}

func TestRawExpectedSizeValidationAndMismatch(t *testing.T) {
	for _, mode := range []string{"negative", "over profile", "json", "header mismatch", "short body", "long body", "zero"} {
		t.Run(mode, func(t *testing.T) {
			e, f, id, dir := streamSetup(t, func(_ context.Context, dst io.Writer) error {
				head := "Content-Type: application/octet-stream\n"
				if mode == "header mismatch" {
					head += "Content-Length: 2\n"
				}
				if _, err := io.WriteString(dst, head+"\n"); err != nil {
					return err
				}
				body := "a"
				if mode == "long body" {
					body = "abcd"
				} else if mode == "zero" {
					body = ""
				}
				_, err := io.WriteString(dst, body)
				return err
			})
			size := int64(3)
			responseType := "raw"
			switch mode {
			case "negative":
				size = -1
			case "over profile":
				size = maxWebAPIRaw + 1
			case "json":
				responseType = "json"
			case "zero":
				size = 0
			}
			r := webCall(e, id, true, map[string]any{"api": "SYNO.SynologyDrive.Files", "method": "download",
				"version": 2, "params": map[string]any{}, "response_type": responseType, "expected_size": size})
			want := "WEBAPI_UNCERTAIN"
			switch mode {
			case "negative", "over profile", "json":
				want = "INVALID_REQUEST"
				if f.calls != 0 {
					t.Fatal("invalid size reached DSM")
				}
			case "long body":
				want = "limit-exceeded"
			case "zero":
				if !r.OK {
					t.Fatal("empty metadata body rejected", r)
				}
				return
			}
			if r.OK || r.Code != want {
				t.Fatalf("%+v, want %s", r, want)
			}
			if files, _ := os.ReadDir(dir); len(files) != 0 || e.rawReserved != 0 {
				t.Fatal("mismatch leaked output/reservation")
			}
		})
	}
}

func TestRawTenGiBHeaderAndProfileBoundary(t *testing.T) {
	for _, size := range []int64{10 << 30, (10 << 30) + 1} {
		e, _, id, dir := streamSetup(t, func(_ context.Context, dst io.Writer) error {
			_, err := io.WriteString(dst, "Content-Type: application/octet-stream\nContent-Length: "+strconv.FormatInt(size, 10)+"\n\n")
			if size == 10<<30 && err != nil {
				t.Error("exact 10 GiB header refused", err)
			}
			// No multi-GiB body is generated. The existing counted-body test
			// exercises the final allowed byte and the first refused byte.
			return err
		})
		e.RootStaging = capacityStaging{dirStaging: dirStaging{dir}, free: 10 << 30}
		r := streamCall(e, id)
		want := "WEBAPI_UNCERTAIN" // accepted header, intentionally absent body
		if size > 10<<30 {
			want = "limit-exceeded"
			profile := e.Policy.WebAPI["calendar"]
			profile.RawMaxBytes = size
			e.Policy.WebAPI["calendar"] = profile
			if validWebAPIProfiles(e.Policy) {
				t.Fatal("profile over 10 GiB accepted")
			}
		}
		if r.Code != want {
			t.Fatalf("size %d: %+v", size, r)
		}
	}
}

func TestRawMetadataDerivesDeadline(t *testing.T) {
	for _, size := range []int64{1 << 30, 5 << 30, 10 << 30} {
		e, _, id, _ := streamSetup(t, func(ctx context.Context, dst io.Writer) error {
			deadline, ok := ctx.Deadline()
			want := time.Duration(30) * time.Minute
			if size == 5<<30 {
				want = 90 * time.Minute
			} else if size == 10<<30 {
				want = 3 * time.Hour
			}
			if !ok || time.Until(deadline) > want || time.Until(deadline) < want-time.Minute {
				t.Error("raw size deadline not applied", time.Until(deadline), want)
			}
			_, err := io.WriteString(dst, "Content-Type: application/octet-stream\n\n")
			return err
		})
		r := webCall(e, id, true, map[string]any{"api": "SYNO.SynologyDrive.Files", "method": "download",
			"version": 2, "params": map[string]any{}, "response_type": "raw", "expected_size": size})
		if r.Code != "WEBAPI_UNCERTAIN" {
			t.Fatal("unexpected result", r)
		}
	}
}
