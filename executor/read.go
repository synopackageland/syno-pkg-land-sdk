package main

import (
	"context"
	"errors"
	"os"
	"strings"
)

// maxReadBytes bounds files.read; it matches the App client's download limit.
const maxReadBytes int64 = 64 << 20

// FileReader copies one regular file, opened as the subject beneath root, into
// a new private file in the package staging directory and names that file.
type FileReader interface {
	Read(ctx context.Context, s Subject, root, relative string, limit int64) (string, int64, error)
}

// readRefusedError is a refusal the read child reports with an SDK code.
type readRefusedError struct{ Code string }

func (e readRefusedError) Error() string { return "read refused: " + e.Code }

func (e *Executor) readFile(ctx context.Context, d Delegation, s Subject, real string, p Params, hasLimit bool) Response {
	limit := maxReadBytes
	if hasLimit {
		if p.MaxBytes <= 0 || p.MaxBytes > maxReadBytes {
			return deny("INVALID_REQUEST")
		}
		limit = p.MaxBytes
	}
	if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() || real == d.Root {
		return deny("not-a-file")
	}
	if e.Reader == nil {
		return deny("READ_FAILED")
	}
	if e.ReadStaging != nil {
		release, err := e.reserveReadStaging(limit)
		if err != nil {
			if errors.Is(err, errRawSpace) {
				return deny("NO_SPACE")
			}
			return deny("STORAGE_ERROR")
		}
		defer release()
	}
	done, code := e.beginLegacy(d)
	if code != "" {
		return deny(code)
	}
	defer done()
	name, size, err := e.Reader.Read(ctx, s, d.Root, strings.TrimPrefix(real, d.Root+"/"), limit)
	var refused readRefusedError
	if errors.As(err, &refused) {
		return deny(refused.Code)
	}
	if errors.Is(err, errRawSpace) {
		return deny("NO_SPACE")
	}
	if err != nil {
		return deny("READ_FAILED")
	}
	reply := map[string]any{"staging": name, "size": size}
	if e.ReadStaging != nil {
		reply["memory_staging"] = true
	}
	return success(reply)
}
