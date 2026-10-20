package sharedfolder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// Target is a folder selected in trusted App configuration, not request params.
// Name is the DSM share name shown to the administrator; Path may be a directory
// within that share. A successful check does not certify child paths or later I/O.
type Target struct {
	Key, Name, Path string
	Required        Access
}

func (t Target) validate() error {
	if !validName(t.Name) || !filepath.IsAbs(t.Path) || (t.Required != Read && t.Required != Write) {
		return fmt.Errorf("invalid shared folder target")
	}
	return nil
}

// Check measures access without creating files or changing ACLs. Run as the
// package account. It is an advisory preflight, not an authorization decision.
func (c Config) Check(target Target) error {
	account, err := c.account()
	if err != nil {
		return err
	}
	_, err = check(target, account)
	return err
}

func check(target Target, account string) (Access, error) {
	if err := target.validate(); err != nil {
		return None, err
	}
	info, err := os.Stat(target.Path)
	if err != nil {
		if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
			return None, &PermissionError{Key: target.Key, Name: target.Name, Account: account, Need: target.Required, Have: None, Cause: err}
		}
		return None, err
	}
	if !info.IsDir() {
		return None, fmt.Errorf("shared folder target is not a directory")
	}
	have, err := granted(target.Path)
	if err != nil {
		return have, err
	}
	if !have.Satisfies(target.Required) {
		return have, &PermissionError{Key: target.Key, Name: target.Name, Account: account, Need: target.Required, Have: have}
	}
	return have, nil
}

// WrapError translates a real EACCES/EPERM failure using the same error as
// Check. It does not recheck access (a child ACL may differ) or mask other errors.
func (c Config) WrapError(target Target, err error) error {
	if err == nil || (!errors.Is(err, syscall.EACCES) && !errors.Is(err, syscall.EPERM)) {
		return err
	}
	account, identityErr := c.account()
	if identityErr != nil {
		return identityErr
	}
	if validationErr := target.validate(); validationErr != nil {
		return validationErr
	}
	return &PermissionError{Key: target.Key, Name: target.Name, Account: account, Need: target.Required, Cause: err}
}

// Routes provides sharedfolder.status. Register the returned routes with the
// App's API server. A nil targets callback uses the install wizard's Config.
// The callback reads current trusted storage configuration once per explicit
// request. Neither the caller nor request params choose a path or account.
func (c Config) Routes(targets func(context.Context) ([]Target, error)) []api.Route {
	return []api.Route{api.Handle("sharedfolder.status", func(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(params, &fields) != nil || fields == nil || len(fields) != 0 {
			return nil, api.BadRequest("sharedfolder.status params must be {}")
		}
		account, err := c.account()
		if err != nil {
			return nil, APIError(err)
		}
		if targets == nil {
			status, err := c.Status()
			return status, APIError(err)
		}
		registered, err := targets(ctx)
		if err != nil {
			return nil, err
		}
		status := Status{Account: account, Folders: make([]Folder, 0, len(registered))}
		for _, target := range registered {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := target.validate(); err != nil {
				return nil, err
			}
			folder := Folder{Key: target.Key, Name: target.Name, Required: target.Required, Granted: None, State: Ready}
			have, err := check(target, account)
			var perm *PermissionError
			switch {
			case errors.As(err, &perm):
				folder.State, folder.Exists, folder.Granted = Pending, true, perm.Have
			case errors.Is(err, os.ErrNotExist):
				folder.State, folder.Error = Missing, err.Error()
			case err != nil:
				return nil, APIError(err)
			default:
				folder.Exists, folder.Granted = true, have
			}
			status.Folders = append(status.Folders, folder)
		}
		return status, nil
	})}
}
