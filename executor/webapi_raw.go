package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"mime"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxWebAPIRaw int64 = 2 << 30
const webAPIRawTimeout = 30 * time.Minute
const maxWebAPIStaging = maxWebAPIRaw + maxRootStaging
const inlineWebAPIRaw = 32 << 10
const maxCGIHeaders = 8 << 10

var errCGI = errors.New("invalid WebAPI CGI response")
var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

type webAPIRawCaller interface {
	WebAPIRaw(context.Context, string, string, string, int, map[string]any, io.Writer) error
}

// rawWebOutput strips a bounded CGI preamble before streaming opaque body bytes.
// It retains no header values after parsing except validated content type/length.
type rawWebOutput struct {
	ctx         context.Context
	head        []byte
	parsed      bool
	contentType string
	length      int64
	body        *rootOutput
	hash        hash.Hash
}

func (w *rawWebOutput) Write(p []byte) (int, error) {
	if w.ctx != nil && w.ctx.Err() != nil {
		return 0, w.ctx.Err()
	}
	n := len(p)
	for !w.parsed && len(p) > 0 {
		w.head = append(w.head, p[0])
		p = p[1:]
		if len(w.head) > maxCGIHeaders {
			return 0, errCGI
		}
		if bytes.HasSuffix(w.head, []byte("\r\n\r\n")) || bytes.HasSuffix(w.head, []byte("\n\n")) {
			if err := w.parse(); err != nil {
				return 0, err
			}
			w.parsed = true
			w.head = nil
		}
	}
	if len(p) > 0 {
		if _, err := w.body.Write(p); err != nil {
			return 0, err
		}
		w.hash.Write(p)
	}
	return n, nil
}

func (w *rawWebOutput) parse() error {
	w.contentType = "application/octet-stream"
	w.length = -1
	seen := map[string]bool{}
	lines := strings.Split(strings.TrimSuffix(strings.TrimSuffix(string(w.head), "\n"), "\r"), "\n")
	count := 0
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		count++
		key, value, ok := strings.Cut(line, ":")
		if !ok || !headerName.MatchString(key) || count > 32 {
			return errCGI
		}
		for _, c := range []byte(value) {
			if c < 32 || c > 126 {
				return errCGI
			}
		}
		key = strings.ToLower(key)
		value = strings.TrimSpace(value)
		switch key {
		case "content-type", "content-length", "status", "location", "x-accel-redirect", "transfer-encoding", "content-encoding":
			if seen[key] {
				return errCGI
			}
			seen[key] = true
		}
		switch key {
		case "location", "x-accel-redirect", "transfer-encoding", "content-encoding":
			return errCGI
		case "content-type":
			if len(value) > 256 {
				return errCGI
			}
			if _, _, err := mime.ParseMediaType(value); err != nil {
				return errCGI
			}
			w.contentType = value
		case "content-length":
			if value == "" || strings.Trim(value, "0123456789") != "" {
				return errCGI
			}
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 {
				return errCGI
			}
			if n > w.body.limit {
				return errOutputLimit
			}
			w.length = n
		case "status":
			if len(value) < 3 || (len(value) > 3 && value[3] != ' ') {
				return errCGI
			}
			n, err := strconv.Atoi(value[:3])
			if err != nil || n < 200 || n >= 300 {
				return errCGI
			}
		}
	}
	if count == 0 {
		return errCGI
	}
	return nil
}

func (e *Executor) callWebAPIRaw(ctx context.Context, s Subject, d Delegation, p webAPIRequest, params map[string]any) Response {
	caller, ok := e.DSM.(webAPIRawCaller)
	if !ok {
		return deny("WEBAPI_UNCERTAIN")
	}
	if !e.rootSlot() {
		return deny("ROOT_BUSY")
	}
	defer e.rootSlotDone()
	limit := e.Policy.WebAPI[d.Scope].RawMaxBytes
	e.rootMu.Lock()
	var used int64
	var err error
	if e.RootStaging != nil {
		used, err = e.RootStaging.Used()
	}
	if err != nil || used+e.rawReserved+limit > maxWebAPIStaging {
		e.rootMu.Unlock()
		return deny("STORAGE_ERROR")
	}
	e.rawReserved += limit
	e.rootMu.Unlock()
	defer func() {
		e.rootMu.Lock()
		e.rawReserved -= limit
		e.rootMu.Unlock()
	}()
	out := &rootOutput{limit: limit, spillAt: inlineWebAPIRaw + 1, staging: e.RootStaging, stagingLimit: maxWebAPIStaging, failed: make(chan struct{})}
	defer out.discard()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := &rawWebOutput{ctx: ctx, body: out, hash: sha256.New()}
	target := d.Scope + ":" + p.API + "/" + p.Method + "/" + strconv.Itoa(p.Version) + ":raw"
	e.commitMu.Lock()
	code := e.rawGrantCodeLocked(d)
	if code != "" {
		e.commitMu.Unlock()
		return deny(code)
	}
	if e.rawCalls == nil {
		e.rawCalls = map[string]map[*rawWebOutput]context.CancelFunc{}
	}
	if e.rawCalls[d.ID] == nil {
		e.rawCalls[d.ID] = map[*rawWebOutput]context.CancelFunc{}
	}
	e.rawCalls[d.ID][w] = cancel
	err = e.auditLocked(s, "webapi.call", d.ID, target, "admitting")
	e.commitMu.Unlock()
	defer func() {
		e.commitMu.Lock()
		delete(e.rawCalls[d.ID], w)
		if len(e.rawCalls[d.ID]) == 0 {
			delete(e.rawCalls, d.ID)
		}
		e.commitMu.Unlock()
	}()
	if err != nil {
		return deny("STORAGE_ERROR")
	}
	err = caller.WebAPIRaw(ctx, s.Username, p.API, p.Method, p.Version, params, w)
	code = ""
	switch {
	case errors.Is(err, errOutputLimit):
		code = "limit-exceeded"
	case errors.Is(err, errRootStaging), errors.Is(err, errRootStagingFull):
		code = "STORAGE_ERROR"
	case err != nil || ctx.Err() != nil || !w.parsed || (w.length >= 0 && w.length != out.n):
		code = "WEBAPI_UNCERTAIN"
	}
	result := "completed"
	if code != "" {
		result = code
	}
	e.commitMu.Lock()
	if fence := e.rawGrantCodeLocked(d); fence != "" {
		code, result = fence, fence
	}
	auditErr := e.auditLocked(s, "webapi.call", d.ID, target, result+":"+strconv.FormatInt(out.n, 10))
	e.commitMu.Unlock()
	if auditErr != nil {
		return deny("WEBAPI_UNCERTAIN")
	}
	if code != "" {
		return deny(code)
	}
	reply := map[string]any{"response_type": "raw", "content_type": w.contentType, "size": out.n, "sha256": hex.EncodeToString(w.hash.Sum(nil))}
	if out.file == nil {
		// []byte encodes as base64; empty bodies must encode as "", never null.
		reply["body"] = append([]byte{}, out.mem.Bytes()...)
	} else {
		name, _, err := out.keep()
		if err != nil {
			return deny("STORAGE_ERROR")
		}
		reply["staging"] = name
	}
	return success(reply)
}

// rawGrantCodeLocked fences completion as well as admission. Transfers may
// outlive the old short JSON lease; revoke/disable cancel their running command.
func (e *Executor) rawGrantCodeLocked(d Delegation) string {
	disabled, err := e.Store.Disabled()
	fresh, getErr := e.Store.Get(d.ID)
	if err != nil || getErr != nil {
		return "STORAGE_ERROR"
	}
	if disabled {
		return "disabled"
	}
	if fresh.Revoked || !e.webGrantMatches(fresh) || (fresh.Lifetime != "until-revoked" && !e.Now().Before(fresh.Expires)) {
		return "DELEGATION_DENIED"
	}
	return ""
}

// WebAPIRaw uses the same fixed argv and permanent restrictions as JSON, with
// an explicitly streaming command instead of the bounded JSON command buffer.
func (d *liveDSM) WebAPIRaw(ctx context.Context, runner, api, method string, version int, params map[string]any, out io.Writer) error {
	raw, err := json.Marshal(params)
	if err != nil || runner == "" || deniedWebAPI(api, method) || version < 1 || version > 65535 {
		return errCGI
	}
	if _, code := webAPIParams(raw); code != "" {
		return errCGI
	}
	args, err := webAPIArgs(api, method, strconv.Itoa(version), runner, params)
	if err != nil {
		return err
	}
	// DSM 7.3 MailPlus writes MIME to its outfile, then writes a separate
	// error-117 JSON control reply to stdout (even after a complete MIME).
	// Capture the dedicated body FD; never guess/strip a JSON-looking MIME tail.
	if api == "SYNO.MailClient.Message" && method == "get_original" && version == 10 {
		stream, ok := d.Command.(interface {
			StreamBody(context.Context, string, []string, io.Writer) ([]byte, error)
		})
		if !ok {
			return errCGI
		}
		if _, err = io.WriteString(out, "Content-Type: message/rfc822\r\n\r\n"); err != nil {
			return err
		}
		count := &countWebBody{out: out}
		control, err := stream.StreamBody(ctx, "/usr/syno/bin/synowebapi", args, count)
		if err != nil {
			return err
		}
		var envelope struct {
			Success *bool `json:"success"`
			Error   struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if len(control) > maxWebAPIReply || strictJSON(control, &map[string]json.RawMessage{}) != nil || json.Unmarshal(control, &envelope) != nil || envelope.Success == nil {
			return errCGI
		}
		if !*envelope.Success && !(envelope.Error.Code == 117 && count.n > 0) {
			return errCGI
		}
		return nil
	}
	stream, ok := d.Command.(interface {
		Stream(context.Context, string, []string, io.Writer) error
	})
	if !ok {
		return errCGI
	}
	return stream.Stream(ctx, "/usr/syno/bin/synowebapi", args, out)
}

type countWebBody struct {
	out io.Writer
	n   int64
}

func (w *countWebBody) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	w.n += int64(n)
	return n, err
}
