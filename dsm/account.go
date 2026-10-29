package dsm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"
)

var errNativeAccount = errors.New("DSM account verification unavailable")

// AccountEnabled asks DSM's native SDK whether principal is an existing,
// enabled account that does not expire. An account with any expiry date,
// including a future one, is refused, as is every unknown result.
func AccountEnabled(ctx context.Context, principal string) (bool, error) {
	if !validPrincipal(principal) {
		return false, errNativeAccount
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return nativeAccountEnabled(ctx, principal)
}

// parseNativeAccount reads the account bridge's one JSON object. Only the
// requested name with expiry "normal" is enabled; "now" covers disabled and
// expiring accounts.
func parseNativeAccount(out []byte, principal string, err error) (bool, error) {
	if err != nil {
		return false, errNativeAccount
	}
	var account struct {
		Username string `json:"username"`
		UID      int64  `json:"uid"`
		Expired  string `json:"expired"`
	}
	d := json.NewDecoder(bytes.NewReader(out))
	if d.Decode(&account) != nil || d.More() || account.Username != principal || account.UID < 1024 {
		return false, errNativeAccount
	}
	switch account.Expired {
	case "normal":
		return true, nil
	case "now":
		return false, nil
	}
	return false, errNativeAccount
}
