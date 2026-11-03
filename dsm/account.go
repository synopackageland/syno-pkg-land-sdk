package dsm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"time"
	"unicode/utf8"
)

var (
	// ErrAccountUnavailable means no trustworthy account snapshot was obtained.
	ErrAccountUnavailable = errors.New("DSM account verification unavailable")
	// ErrAccountNotFound reports NSS absence, not authoritative remote deletion.
	ErrAccountNotFound = errors.New("DSM NSS reported no such account")
	// ErrAccountTimeout also matches context.DeadlineExceeded via errors.Is.
	ErrAccountTimeout = errors.New("DSM account lookup timed out")
	errNativeAccount  = ErrAccountUnavailable
)

// DefaultAccountLookupTimeout accommodates slow directory controllers. A
// deadline on the caller's context overrides this default (shorter or longer).
const DefaultAccountLookupTimeout = 15 * time.Second

// AccountSource identifies the verified identity provider, never name syntax.
type AccountSource string

const (
	AccountSourceLocal  AccountSource = "local"
	AccountSourceDomain AccountSource = "domain"
	AccountSourceLDAP   AccountSource = "ldap"
)

// AccountInfo is a single DSM account snapshot, obtained as the package account
// without administrator delegation. UID/GID and Groups retain unsigned DSM IDs.
// Expired is "normal" or "now"; "now" also includes a future expiry date, because
// the native admission policy refuses any account with a configured expiry.
// Disabled and elapsed expiry are not distinguished by this bridge.
type AccountInfo struct {
	Exists   bool          `json:"exists"`
	Username string        `json:"username"`
	UID      uint32        `json:"uid"`
	GID      uint32        `json:"gid"`
	Home     string        `json:"home"`
	Admin    bool          `json:"admin"`
	Expired  string        `json:"expired"`
	Groups   []uint32      `json:"groups"`
	Source   AccountSource `json:"source"`
}

// LookupAccount returns the full native account result, including disabled
// accounts. Errors are ErrAccountNotFound (NSS absence), ErrAccountTimeout
// (also context.DeadlineExceeded), context.Canceled, or ErrAccountUnavailable.
// Every error returns an empty result; it must not be interpreted as disabled
// or as permission to delete a stored person. NSS absence is not proof that a
// remote directory deleted the account. Resolved LDAP identities are refused:
// they have no verified online/expiry path in this bridge.
func LookupAccount(ctx context.Context, principal string) (AccountInfo, error) {
	return lookupAccount(ctx, principal, nativeAccountLookup)
}

func lookupAccount(ctx context.Context, principal string, read func(context.Context, string) ([]byte, error)) (AccountInfo, error) {
	if !validPrincipal(principal) {
		return AccountInfo{}, ErrAccountUnavailable
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultAccountLookupTimeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return AccountInfo{}, accountContextError(err)
	}
	out, err := read(ctx, principal)
	// CommandContext commonly returns "signal: killed", not ctx.Err().
	if ctx.Err() != nil {
		return AccountInfo{}, accountContextError(ctx.Err())
	}
	return parseAccountInfo(out, principal, err)
}

func accountContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(ErrAccountTimeout, err)
	}
	return err
}

// AccountEnabled asks DSM's native SDK whether principal is an existing,
// enabled account that does not expire. An account with any expiry date,
// including a future one, is refused, as is every unknown result.
func AccountEnabled(ctx context.Context, principal string) (bool, error) {
	account, err := LookupAccount(ctx, principal)
	return err == nil && account.Expired == "normal", err
}

func parseAccountInfo(out []byte, principal string, err error) (AccountInfo, error) {
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 3 {
			return AccountInfo{}, ErrAccountNotFound
		}
		return AccountInfo{}, ErrAccountUnavailable
	}
	var wire struct {
		Username string        `json:"username"`
		UID      *uint32       `json:"uid"`
		GID      *uint32       `json:"gid"`
		Home     *string       `json:"home"`
		Admin    *bool         `json:"admin"`
		Expired  string        `json:"expired"`
		Groups   []*uint32     `json:"groups"`
		Source   AccountSource `json:"source"`
	}
	d := json.NewDecoder(bytes.NewReader(out))
	d.DisallowUnknownFields()
	if !utf8.Valid(out) || d.Decode(&wire) != nil || d.Decode(new(any)) != io.EOF ||
		wire.Username != principal || wire.UID == nil || *wire.UID < 1024 || *wire.UID == ^uint32(0) ||
		wire.GID == nil || *wire.GID == ^uint32(0) || wire.Home == nil || wire.Admin == nil ||
		len(wire.Groups) == 0 || len(wire.Groups) > 1024 ||
		(wire.Source != AccountSourceLocal && wire.Source != AccountSourceDomain) ||
		(wire.Expired != "normal" && wire.Expired != "now") {
		return AccountInfo{}, ErrAccountUnavailable
	}
	primary := false
	groups := make([]uint32, len(wire.Groups))
	for i, gid := range wire.Groups {
		if gid == nil || *gid == ^uint32(0) {
			return AccountInfo{}, ErrAccountUnavailable
		}
		groups[i] = *gid
		primary = primary || *gid == *wire.GID
	}
	if !primary {
		return AccountInfo{}, ErrAccountUnavailable
	}
	return AccountInfo{Exists: true, Username: wire.Username, UID: *wire.UID, GID: *wire.GID,
		Home: *wire.Home, Admin: *wire.Admin, Expired: wire.Expired, Groups: groups, Source: wire.Source}, nil
}
