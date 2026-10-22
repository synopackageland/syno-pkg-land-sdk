// Package sharedfolder tells an App which Shared Folders the install wizard
// stored and what the package account may do in each. The wizard records only
// the folder name; DSM does not grant the package account access to a folder
// chosen at install time, so an administrator does that afterwards (see
// docs/shared-folder.md). The App and its daemon read the same state here:
// the answer comes from the kernel's check for the account the process runs as,
// never from a DSM setting, and nothing in this package needs root.
package sharedfolder

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

var (
	ErrNotConfigured     = errors.New("shared folder is not configured")
	ErrNotFound          = errors.New("shared folder not found on any volume")
	ErrAmbiguous         = errors.New("shared folder name matches more than one volume")
	ErrPermissionPending = errors.New("package account lacks permission on the shared folder")
	ErrIdentity          = errors.New("shared folder checks require the matching non-root package account")
)

// Access is what the package account may do in a Shared Folder.
type Access string

const (
	None  Access = "none"
	Read  Access = "read"
	Write Access = "write" // read and write
)

func (a Access) rank() int {
	switch a {
	case Write:
		return 2
	case Read:
		return 1
	}
	return 0
}

// Satisfies reports whether a is at least need.
func (a Access) Satisfies(need Access) bool { return a.rank() >= need.rank() }

// State is the relation between what the App needs and what the account has.
type State string

const (
	// Ready: the account has the access the App declared.
	Ready State = "ready"
	// Pending: the share exists but an administrator must grant the account
	// more access ("pending authorization").
	Pending State = "pending"
	// Missing: no volume has the share (deleted or renamed); choosing another
	// folder is the fix, not a permission.
	Missing State = "missing"
	// Ambiguous: more than one volume has a folder of that name.
	Ambiguous State = "ambiguous"
)

// Folder is one configured Shared Folder right now.
type Folder struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Exists   bool   `json:"exists"`
	Required Access `json:"required"`
	Granted  Access `json:"granted"`
	State    State  `json:"state"`
	Error    string `json:"error,omitempty"`
}

// Status is every configured Shared Folder, sorted by key, with the account a
// grant must name.
type Status struct {
	Account string   `json:"account"`
	Folders []Folder `json:"folders"`
}

// Pending reports whether any folder waits for an administrator's grant.
func (s Status) Pending() bool {
	for _, f := range s.Folders {
		if f.State == Pending {
			return true
		}
	}
	return false
}

// Config says where the installer's state lives and what the App needs.
type Config struct {
	// DataDir is the package's persistent directory (/var/packages/<id>/var).
	DataDir string
	// Root is where /volumeN lives; empty means "/". Tests set it.
	Root string
	// Required is the access each key needs, as dsm-package.yaml
	// install.sharedFolder.permission declares it (ro = Read, rw = Write). A
	// key without an entry needs Read.
	Required map[string]Access
	// Account, when set, must match the account this process runs as. It is
	// never the interactive caller and never changes the process identity.
	Account string
}

func validName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "/\\\x00") && !strings.Contains(name, "..") &&
		!strings.HasPrefix(name, "@") && !strings.HasPrefix(name, ".")
}

// Names returns the Shared Folder names the installer stored, by key. A package
// without config.json has none.
func Names(dataDir string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Schema        int               `json:"schema"`
		SharedFolders map[string]string `json:"shared_folders"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config.json: %w", err)
	}
	if cfg.Schema != 1 {
		return nil, fmt.Errorf("config.json: unsupported schema %d", cfg.Schema)
	}
	for key, name := range cfg.SharedFolders {
		if !validName(name) {
			return nil, fmt.Errorf("config.json: invalid shared folder name for %q", key)
		}
	}
	return cfg.SharedFolders, nil
}

// Resolve finds /volumeN/<name> under root ("/" on a NAS). The path is looked
// up on every call, so a share moved to another volume keeps working.
func Resolve(root, name string) (string, error) {
	if !validName(name) {
		return "", fmt.Errorf("invalid shared folder name %q", name)
	}
	volumes, err := filepath.Glob(filepath.Join(root, "volume[0-9]*"))
	if err != nil {
		return "", err
	}
	var found []string
	var inaccessible error
	for _, volume := range volumes {
		path := filepath.Join(volume, name)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			found = append(found, path)
		} else if errors.Is(err, fs.ErrPermission) {
			inaccessible = err
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	// An unsearchable volume is not evidence that the selected share vanished,
	// nor that an accessible match on another volume is unambiguous.
	if inaccessible != nil {
		return "", inaccessible
	}
	switch len(found) {
	case 0:
		return "", ErrNotFound
	case 1:
		return found[0], nil
	}
	return "", ErrAmbiguous
}

const (
	accessRead  = 0x4 // R_OK
	accessWrite = 0x2 // W_OK
	accessExec  = 0x1 // X_OK
)

// granted asks the kernel what this process's account may do in dir. DSM
// applies the share's permission and ACL there, so it covers group
// membership, deny rows and Windows ACL that a DSM row listing cannot decide.
func granted(dir string) (Access, error) {
	if err := syscall.Access(dir, accessRead|accessExec); err != nil {
		if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
			return None, nil
		}
		return None, err
	}
	if err := syscall.Access(dir, accessWrite|accessExec); err != nil {
		if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
			return Read, nil
		}
		return Read, err
	}
	return Write, nil
}

func (c Config) root() string {
	if c.Root == "" {
		return "/"
	}
	return c.Root
}

func (c Config) account() (string, error) {
	if os.Getuid() == 0 || os.Geteuid() == 0 || os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() {
		return "", ErrIdentity
	}
	u, err := user.Current()
	if err != nil || u.Username == "" || (c.Account != "" && c.Account != u.Username) {
		return "", ErrIdentity
	}
	return u.Username, nil
}

func (c Config) required(key string) Access {
	if need := c.Required[key]; need == Read || need == Write {
		return need
	}
	return Read
}

// Status reports every configured Shared Folder. The error is a corrupt
// config.json.
func (c Config) Status() (Status, error) {
	account, err := c.account()
	if err != nil {
		return Status{}, err
	}
	names, err := Names(c.DataDir)
	if err != nil {
		return Status{}, err
	}
	keys := make([]string, 0, len(names))
	for key := range names {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	status := Status{Account: account, Folders: make([]Folder, 0, len(keys))}
	for _, key := range keys {
		folder := Folder{Key: key, Name: names[key], Required: c.required(key), Granted: None}
		path, err := Resolve(c.root(), folder.Name)
		switch {
		case errors.Is(err, fs.ErrPermission):
			folder.State = Pending
		case errors.Is(err, ErrAmbiguous):
			folder.State, folder.Error = Ambiguous, err.Error()
		case err != nil:
			folder.State, folder.Error = Missing, err.Error()
		default:
			folder.Granted, err = granted(path)
			if err != nil {
				return Status{}, err
			}
			folder.Path, folder.Exists = path, true
			folder.State = Pending
			if folder.Granted.Satisfies(folder.Required) {
				folder.State = Ready
			}
		}
		status.Folders = append(status.Folders, folder)
	}
	return status, nil
}

// PermissionError says exactly what an administrator must grant. It matches
// ErrPermissionPending.
type PermissionError struct {
	Key, Name, Account string
	Need, Have         Access
	Cause              error
}

func (e *PermissionError) Error() string {
	access := "Read only"
	if e.Need == Write {
		access = "Read/Write"
	}
	return fmt.Sprintf("Package account %q needs %s access to shared folder %q. Ask an administrator to open Control Panel > Shared Folder > %s > Edit > Permissions > System internal user > %s > %s", e.Account, access, e.Name, e.Name, e.Account, access)
}

func (e *PermissionError) Is(target error) bool { return target == ErrPermissionPending }

// Both the original I/O failure and its public API representation remain
// available, including when an App handler returns PermissionError directly.
func (e *PermissionError) Unwrap() []error {
	if e.Cause == nil {
		return []error{e.apiError()}
	}
	return []error{e.Cause, e.apiError()}
}

func (e *PermissionError) apiError() error {
	return api.WithDetails(&api.Error{Code: "shared_folder_permission", Message: e.Error()}, map[string]string{
		"account": e.Account, "share": e.Name, "required": string(e.Need),
	})
}

// Require returns the folder's current path once the account has the access
// the App declared for key, so a daemon fails with a clear error instead of a
// failed file read.
func (c Config) Require(key string) (string, error) { return c.RequireAccess(key, c.required(key)) }

// RequireAccess is Require for an operation that needs less (or more) than
// the declared access.
func (c Config) RequireAccess(key string, need Access) (string, error) {
	account, err := c.account()
	if err != nil {
		return "", err
	}
	names, err := Names(c.DataDir)
	if err != nil {
		return "", err
	}
	name, ok := names[key]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrNotConfigured, key)
	}
	path, err := Resolve(c.root(), name)
	if errors.Is(err, fs.ErrPermission) {
		return "", &PermissionError{Key: key, Name: name, Account: account, Need: need, Have: None, Cause: err}
	}
	if err != nil {
		return "", fmt.Errorf("%w: %q", err, name)
	}
	have, err := granted(path)
	if err != nil {
		return "", err
	}
	if !have.Satisfies(need) {
		return "", &PermissionError{Key: key, Name: name, Account: account, Need: need, Have: have}
	}
	return path, nil
}

// APIError turns this package's errors into API errors a handler may return,
// each with a stable code; any other error passes through.
func APIError(err error) error {
	var perm *PermissionError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &perm):
		return perm.apiError()
	case errors.Is(err, ErrIdentity):
		return &api.Error{Code: "shared_folder_identity", Message: ErrIdentity.Error()}
	case errors.Is(err, ErrNotConfigured):
		return &api.Error{Code: "shared_folder_not_configured", Message: err.Error()}
	case errors.Is(err, ErrNotFound):
		return &api.Error{Code: "shared_folder_not_found", Message: err.Error()}
	case errors.Is(err, ErrAmbiguous):
		return &api.Error{Code: "shared_folder_ambiguous", Message: err.Error()}
	}
	return err
}
