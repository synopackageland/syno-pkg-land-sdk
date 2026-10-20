// Package portal reads the DSM Application Portal alias an administrator set
// for this package. DSM owns the setting: the package never writes it, and
// anything this reader does not fully understand reads as Unavailable.
package portal

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"regexp"
	"syscall"
	"unicode/utf8"

	"github.com/synopackageland/syno-pkg-land-sdk/jsonlimits"
)

// MaxConfigBytes bounds DSM's saved setting; its real size is a few hundred bytes.
const MaxConfigBytes = 64 << 10

const (
	// Active means users can open the app at /<alias>/ after DSM sign-in.
	Active = "active"
	// Inactive means no administrator has set an alias yet.
	Inactive = "inactive"
	// Unavailable means the setting could not be read safely.
	Unavailable = "unavailable"
)

// Status is what portal.status returns; Alias is set only when Active.
// Entry is set for an App whose executor registers its entry (WithEntry).
type Status struct {
	State string `json:"state"`
	Alias string `json:"alias,omitempty"`
	Entry string `json:"entry,omitempty"`
}

// A single URL path segment; DSM's own validation is looser than this.
var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ConfigPath is where DSM saves the package's portal setting
// (/var/packages/<package>/etc is the package's @appconf directory).
func ConfigPath(packageID string) string {
	return "/var/packages/" + packageID + "/etc/AppPortal.json"
}

// Read returns the saved alias when the setting belongs to appID.
func Read(path, appID string) Status {
	raw, err := readBounded(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Status{State: Inactive}
	}
	if err != nil {
		return Status{State: Unavailable}
	}
	id, alias, ok := parse(raw)
	if !ok || id != appID {
		return Status{State: Unavailable}
	}
	if alias == "" {
		return Status{State: Inactive}
	}
	if !aliasPattern.MatchString(alias) {
		return Status{State: Unavailable}
	}
	return Status{State: Active, Alias: alias}
}

func readBounded(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxConfigBytes {
		return nil, errors.New("too large")
	}
	return raw, nil
}

// parse accepts exactly one JSON object with unique top-level keys, a string
// id and a string or null alias; other DSM fields are skipped unread.
func parse(raw []byte) (id, alias string, ok bool) {
	if !utf8.Valid(raw) || !jsonlimits.WithinLimits(raw) {
		return "", "", false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return "", "", false
	}
	seen := map[string]bool{}
	hasID := false
	for d.More() {
		token, err := d.Token()
		key, isKey := token.(string)
		if err != nil || !isKey || seen[key] {
			return "", "", false
		}
		seen[key] = true
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return "", "", false
		}
		switch key {
		case "id":
			if json.Unmarshal(value, &id) != nil || id == "" {
				return "", "", false
			}
			hasID = true
		case "alias":
			var text *string
			if json.Unmarshal(value, &text) != nil {
				return "", "", false
			}
			if text != nil {
				alias = *text
			}
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return "", "", false
	}
	if _, err := d.Token(); err != io.EOF {
		return "", "", false
	}
	return id, alias, hasID
}
