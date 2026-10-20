package filestation

import (
	"context"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"io"
	"log"
	"mime"
	"net/http"
	"path"
	"strconv"
)

// Raw upload bodies are passed through as readers, with no multipart buffering.
func (s *Service) uploadHTTP(ctx context.Context, c api.Caller, w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return &api.Error{Code: "method_not_allowed", Message: "use POST"}
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/octet-stream" {
		return fail("invalid-request")
	}
	if r.ContentLength < 0 || r.ContentLength > MaxUploadBytes {
		return fail("upload_size_required")
	}
	q := r.URL.Query()
	if !validPath(q.Get("parent")) || !validName(q.Get("name")) {
		return fail("invalid-request")
	}
	if err := s.Upload(ctx, c, r, q.Get("parent"), q.Get("name"), http.MaxBytesReader(w, r.Body, MaxUploadBytes), r.ContentLength); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	payload := `{"success":true,"data":{"uploaded":true}}`
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	_, err = io.WriteString(w, payload)
	return err
}
func (s *Service) downloadHTTP(ctx context.Context, c api.Caller, w http.ResponseWriter, r *http.Request) error {
	if r.Method == http.MethodPost {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/x-www-form-urlencoded" {
			return fail("invalid-request")
		}
	} else if r.Method != http.MethodGet {
		return &api.Error{Code: "method_not_allowed", Message: "use GET or POST"}
	}
	target := r.URL.Query().Get("path")
	stream, err := s.Download(ctx, c, r, target)
	if err != nil {
		return err
	}
	defer stream.Body.Close()
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(target)}))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	if stream.ContentLength >= 0 {
		h.Set("Content-Length", strconv.FormatInt(stream.ContentLength, 10))
	}
	w.WriteHeader(http.StatusOK)
	// Once headers are sent a failed transfer must remain truncated, never receive a JSON trailer.
	if _, err := io.Copy(w, stream.Body); err != nil {
		log.Print("File Station download interrupted")
	}
	return nil
}
