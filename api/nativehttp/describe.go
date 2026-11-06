package nativehttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// NewDescriptionHandler loads only trusted private package files, snapshots the
// validated metadata and reuses existing SDK DSM/session/entry guards. File paths
// must come from the generated CGI entry, never from request parameters.
// Use the normal DSM authenticator, wrapped with dsm.NewEntryGate for the package
// entry mode. This handler cannot execute an operation's native HTTP binding.
func NewDescriptionHandler(auth api.Authenticator, descriptorPath, inventoryPath string) (http.Handler, error) {
	if auth == nil {
		return nil, errors.New("metadata describe requires DSM authentication")
	}
	db, e := readPackaged(descriptorPath)
	if e != nil {
		return nil, e
	}
	ib, e := readPackaged(inventoryPath)
	if e != nil {
		return nil, e
	}
	a, e := ValidateArtifacts(db, ib)
	if e != nil {
		return nil, e
	}
	mode := a.Descriptor.Payload.Entry
	if mode == "policy" {
		mode = ""
	}
	if gate, ok := auth.(api.EntryAuthenticator); ok {
		if gate.EntryMode() != mode {
			return nil, errors.New("describe authenticator differs from packaged entry mode")
		}
	} else if mode == "app-privilege" {
		return nil, errors.New("describe requires a DSM application privilege entry gate")
	}
	// Enforce admin-only even when the caller provided a plain DSM authenticator.
	guarded := &metadataEntry{Authenticator: auth, mode: mode}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": map[string]string{"code": "unknown_method", "message": "metadata describe only"}})
	})
	return api.WithRequestDeadline(api.WithDescriptionData(next, guarded, db)), nil
}

type metadataEntry struct {
	api.Authenticator
	mode string
}

func (g *metadataEntry) EntryMode() string { return g.mode }
func (g *metadataEntry) Authenticate(r *http.Request) (api.Identity, error) {
	id, e := g.Authenticator.Authenticate(r)
	if e == nil && g.mode == "admin-only" && !id.Admin {
		return api.Identity{}, api.ErrAdminRequired
	}
	return id, e
}
func readPackaged(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, errors.New("packaged native HTTP metadata unavailable")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return nil, errors.New("invalid packaged native HTTP metadata file")
	}
	b, e := io.ReadAll(f)
	if e != nil {
		return nil, errors.New("packaged native HTTP metadata unavailable")
	}
	return b, nil
}
