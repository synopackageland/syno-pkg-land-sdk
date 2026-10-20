package filestation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"net/http"
	"time"
)

// CheckPermission is an online write check with the current user's session.
// It creates no probe file and does not grant any offline authority.
func (c *Client) CheckPermission(ctx context.Context, r *http.Request, path string) error {
	auth, err := api.DSMSession(r)
	if err != nil {
		return err
	}
	r = r.Clone(ctx)
	r.Header.Set("X-SYNO-TOKEN", auth["dsm_synotoken"])
	if !validPath(path) {
		return fail("invalid-request")
	}
	v := fields("SYNO.FileStation.CheckPermission", "write", "3")
	v.Set("path", path)
	v.Set("filename", probeName())
	v.Set("overwrite", "false")
	v.Set("size", "0")
	var data map[string]json.RawMessage
	if err := c.call(ctx, r, v, &data); err != nil {
		return err
	}
	var skip *bool
	if json.Unmarshal(data["blSkip"], &skip) != nil || skip == nil || *skip {
		return fail("denied")
	}
	// File Station can return per-file refusal inside a success envelope.
	for _, key := range []string{"error", "errors"} {
		if raw := data[key]; len(raw) != 0 && string(raw) != "null" && string(raw) != "[]" {
			return fail("denied")
		}
	}
	return nil
}

// probeName is unique per check, so a file left in the folder under an earlier
// probe name cannot make the check skip.
func probeName() string {
	random := make([]byte, 16)
	rand.Read(random)
	return ".synopkgland-permission-probe-" + hex.EncodeToString(random)
}

// Keep the original DSMClient contract usable by existing read-only adapters.
// An adapter lacking the new method refuses the write check.
type permissionClient interface {
	CheckPermission(context.Context, *http.Request, string) error
}

func (s *Service) CheckPermission(ctx context.Context, caller api.Caller, r *http.Request, path string) error {
	if !validPath(path) {
		return fail("invalid-request")
	}
	auth, err := api.DSMSession(r)
	if err != nil {
		return err
	}
	r = r.Clone(ctx)
	r.Header.Set("X-SYNO-TOKEN", auth["dsm_synotoken"])
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.verify(ctx, caller, r); err != nil {
		return err
	}
	client, ok := s.client.(permissionClient)
	if !ok {
		return fail("unsupported")
	}
	return client.CheckPermission(ctx, r, path)
}
