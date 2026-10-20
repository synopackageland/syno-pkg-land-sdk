package filestation

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MaxUploadBytes bounds an interactive upload from a trusted application's
// reader. This is not a background grant; DSM checks the current session.
const MaxUploadBytes int64 = 16 << 30

// UploadStream uploads a server-selected reader without buffering file bytes.
// Never accept an unverified browser filesystem path as the reader's source.
// No overwrite, retries, parent creation or retained session. A lost response
// is commit-uncertain, including a timeout after a partially written upload.
func (c *Client) UploadStream(ctx context.Context, r *http.Request, parent, name string, source io.Reader, size int64) error {
	if c == nil || r == nil || source == nil || !validPath(parent) || !validName(name) || size < 0 || size > MaxUploadBytes {
		return fail("invalid-request")
	}
	cookies := r.CookiesNamed("id")
	token := r.Header.Get("X-SYNO-TOKEN")
	if len(cookies) != 1 || cookies[0].Value == "" || token == "" || len(cookies[0].Value) > 4096 || len(token) > 4096 || strings.ContainsAny(token, "\r\n\x00") {
		return fail("session_required")
	}
	var b bytes.Buffer
	m := multipart.NewWriter(&b)
	for _, f := range [][2]string{{"api", "SYNO.FileStation.Upload"}, {"method", "upload"}, {"version", "2"}, {"path", parent}, {"create_parents", "false"}, {"size", strconv.FormatInt(size, 10)}, {"SynoToken", token}} {
		if e := m.WriteField(f[0], f[1]); e != nil {
			return fail("invalid-request")
		}
	}
	if _, e := m.CreateFormFile("file", name); e != nil {
		return fail("invalid-request")
	}
	prefixLen := b.Len()
	if e := m.Close(); e != nil {
		return fail("invalid-request")
	}
	framing := b.Bytes()
	body := io.MultiReader(bytes.NewReader(framing[:prefixLen]), io.LimitReader(source, size), bytes.NewReader(framing[prefixLen:]))
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, body)
	if e != nil {
		return fail("invalid-request")
	}
	req.ContentLength = int64(len(framing)) + size
	req.Header.Set("Content-Type", m.FormDataContentType())
	req.AddCookie(cookies[0])
	req.Header.Set("X-SYNO-TOKEN", token)
	client := *c.http
	client.Timeout = 20 * time.Minute
	// Upload response headers arrive after DSM receives and commits the file.
	// Keep the metadata deadline unchanged for other requests on this client.
	if transport, ok := client.Transport.(*http.Transport); ok {
		transport = transport.Clone()
		transport.ResponseHeaderTimeout = 20 * time.Minute
		client.Transport = transport
		defer transport.CloseIdleConnections()
	}
	resp, e := client.Do(req)
	if e != nil {
		return fail("commit-uncertain")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fail("commit-uncertain")
	}
	return mutationOutcome(decode(resp, nil))
}
