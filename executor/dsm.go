package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/synopackageland/syno-pkg-land-sdk/dsm"
)

type Command interface {
	Exec(context.Context, string, []string) ([]byte, error)
}
type Accounts interface{ Lookup(string) (Subject, error) }
type localAccounts struct{}

func (localAccounts) Lookup(name string) (Subject, error) {
	var s Subject
	u, err := user.Lookup(name)
	if err != nil || u.Username != name {
		return s, errors.New("account")
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return s, err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return s, err
	}
	gs, err := u.GroupIds()
	if err != nil {
		return s, err
	}
	s = Subject{Username: name, UID: uid, GID: gid, Home: u.HomeDir}
	for _, v := range gs {
		g, err := strconv.Atoi(v)
		if err != nil {
			return Subject{}, err
		}
		s.Groups = append(s.Groups, g)
	}
	sort.Ints(s.Groups)
	return s, nil
}

type liveDSM struct {
	NativeAccount func(context.Context, string) (Subject, error)
	Administrator func(context.Context, string) (bool, error)
	Command       Command
	Accounts      Accounts
	session       func(context.Context, string, []string) ([]byte, error)
	// AppPriv is DSM's native SLIBAppPrivUserHas(principal, app, addr).
	AppPriv func(ctx context.Context, principal, app, addr string) (bool, error)
	// controlIDs is the Task Scheduler id each approval task name had in the
	// last list, for approval checks that get it instead of listing again.
	controlIDs sync.Map
}

// newDSM stages the App Privileges bridge in bridgeDir, a root-only
// directory, when the kernel has no memfd_create.
func newDSM(bridgeDir string) *liveDSM {
	dsm.SetBridgeFallbackDir(bridgeDir)
	return &liveDSM{Administrator: dsm.IsAdministrator, NativeAccount: nativeAccountReader(bridgeDir), Command: processCommand{}, Accounts: localAccounts{}, session: runSessionCGI, AppPriv: appPrivBridge{Dir: bridgeDir}.Check}
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

// dsmError keeps the DSM error code of a refused call; the message stays generic.
type dsmError struct{ Code int }

func (*dsmError) Error() string { return "DSM refused" }

func decodeEnvelope(b []byte) (json.RawMessage, error) {
	data, err := decodeSuccess(b)
	if err == nil && (len(data) == 0 || string(data) == "null") {
		return nil, &dsmError{}
	}
	return data, err
}

// decodeSuccess accepts a success without data: scheduler deletes answer
// {"success":true}, sometimes with httpd_restart, and that confirms the delete.
func decodeSuccess(b []byte) (json.RawMessage, error) {
	var r envelope
	d := json.NewDecoder(bytes.NewReader(b))
	if uniqueValue(d, false, false) != nil || hasDSMError(b) || json.Unmarshal(b, &r) != nil || !r.Success {
		var refused struct {
			Error json.RawMessage `json:"error"`
		}
		json.Unmarshal(b, &refused)
		code := errorCode(refused.Error)
		if code == 0 && json.Unmarshal(b, &r) == nil {
			code = embeddedCode(r.Data)
		}
		return nil, &dsmError{Code: code}
	}
	return r.Data, nil
}
func (d *liveDSM) call(ctx context.Context, api, method, version, runner string, params map[string]any) (json.RawMessage, error) {
	b, err := d.exec(ctx, api, method, version, runner, params)
	if err != nil {
		return nil, err
	}
	return decodeEnvelope(b)
}

// mutate runs a call whose success carries no data, such as a scheduler delete.
func (d *liveDSM) mutate(ctx context.Context, api, method, version string, params map[string]any) error {
	b, err := d.exec(ctx, api, method, version, "", params)
	if err == nil {
		_, err = decodeSuccess(b)
	}
	return err
}
func (d *liveDSM) exec(ctx context.Context, api, method, version, runner string, params map[string]any) ([]byte, error) {
	args, err := webAPIArgs(api, method, version, runner, params)
	if err != nil {
		return nil, err
	}
	b, err := d.Command.Exec(ctx, "/usr/syno/bin/synowebapi", args)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, errors.New("DSM command")
	}
	return b, nil
}

func webAPIArgs(api, method, version, runner string, params map[string]any) ([]string, error) {
	args := []string{"--exec", "api=" + api, "method=" + method, "version=" + version}
	if runner != "" {
		args = append(args, "runner="+runner)
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b, err := json.Marshal(params[k])
		if err != nil {
			return nil, err
		}
		args = append(args, k+"="+string(b))
	}
	return args, nil
}

// verificationUnavailable distinguishes an unknown DSM result from a denial.
var verificationUnavailable = errors.New("DSM verification unavailable")

func verificationError(err error) error { return fmt.Errorf("%w: %w", verificationUnavailable, err) }

func dsmRefusal(err error, denied string) string {
	if errors.Is(err, verificationUnavailable) {
		return "INTERNAL"
	}
	return denied
}

func (d *liveDSM) Account(ctx context.Context, name string) (Subject, error) {
	s, err := d.account(ctx, name)
	if err != nil {
		return Subject{}, err
	}
	role := d.Administrator
	if role == nil {
		role = dsm.IsAdministrator
	}
	// Ignore any account snapshot's role bit; every caller uses the shared DSM provider.
	s.Admin, err = role(ctx, name)
	if err != nil {
		return Subject{}, verificationError(err)
	}
	return s, nil
}

func (d *liveDSM) account(ctx context.Context, name string) (Subject, error) {
	if d.NativeAccount != nil {
		s, err := d.NativeAccount(ctx, name)
		if err != nil {
			return Subject{}, verificationError(err)
		}
		if s.Username != name || s.Expired != "normal" {
			return Subject{}, errors.New("account ineligible")
		}
		return s, nil
	}
	s, err := d.Accounts.Lookup(name)
	if err != nil {
		return s, err
	}
	data, err := d.call(ctx, "SYNO.Core.User", "get", "1", "", map[string]any{"name": name, "additional": []string{"expired"}})
	if err != nil {
		return Subject{}, err
	}
	var out struct {
		Users []struct {
			Name    string `json:"name"`
			UID     *int   `json:"uid"`
			Expired string `json:"expired"`
		} `json:"users"`
	}
	if json.Unmarshal(data, &out) != nil || len(out.Users) != 1 || out.Users[0].Name != name || out.Users[0].UID == nil || *out.Users[0].UID != s.UID || out.Users[0].Expired != "normal" {
		return Subject{}, errors.New("account ineligible")
	}
	s.Expired = out.Users[0].Expired
	return s, nil
}

// Allow mirrors dsm.EntryGate: verified administrators do not depend on App
// Privileges registration. Ordinary users in app-privilege/legacy mode use
// SLIBAppPrivUserHas with the verified client address. An unknown result is
// unavailable, while an explicit false result is a denial.
func (d *liveDSM) Allow(ctx context.Context, s Subject, app, entry string) error {
	if !contains([]string{"", "admin-only", "app-privilege"}, entry) {
		return errors.New("entry mode")
	}
	addr, ok := clientAddress(ctx)
	if ip, err := netip.ParseAddr(addr); !ok || err != nil || ip.Zone() != "" || ip.String() != addr || ip.Is4In6() {
		return errors.New("client address")
	}
	if s.Username == "" || app == "" {
		return errors.New("app privilege")
	}
	if ctx.Err() != nil {
		return verificationError(ctx.Err())
	}
	if s.Admin {
		return nil
	}
	if entry == "admin-only" {
		return errors.New("app denied")
	}
	if d.AppPriv == nil {
		return verificationError(errAppPriv)
	}
	allowed, err := d.AppPriv(ctx, s.Username, app, addr)
	if err != nil {
		if ctx.Err() != nil {
			return verificationError(ctx.Err())
		}
		return verificationError(err)
	}
	if !allowed {
		return errors.New("app denied")
	}
	return nil
}

func (d *liveDSM) ScopeRoot(ctx context.Context, s Subject, scope string) (string, error) {
	if scope == "home" {
		if !cleanPath(s.Home) || s.Home == "/" {
			return "", errors.New("home")
		}
		return filepath.EvalSymlinks(s.Home)
	}
	if scopeVirtual(scope) == "" {
		return "", errors.New("scope")
	}
	name := strings.TrimPrefix(scope, "shared:")
	data, err := d.call(ctx, "SYNO.Core.Share", "get", "1", "", map[string]any{"name": name})
	if err != nil {
		return "", err
	}
	// DSM Share.get returns one share and its volume, not Share.list's array.
	var out struct {
		Name   string `json:"name"`
		Volume string `json:"vol_path"`
	}
	if json.Unmarshal(data, &out) != nil || out.Name != name || !validSelectedScope(scope) || !cleanPath(out.Volume) || out.Volume == "/" {
		return "", errors.New("share")
	}
	return filepath.EvalSymlinks(filepath.Join(out.Volume, out.Name))
}

// userCalls is the root-side allowlist of Web API calls an executor makes as a
// delegated user: api -> method -> version. Nothing else reaches synowebapi
// with a runner, so a bug in an operation cannot become a generic DSM call.
var userCalls = map[string]map[string]string{
	"SYNO.FileStation.List":            {"list": "2", "getinfo": "2"},
	"SYNO.FileStation.CreateFolder":    {"create": "2"},
	"SYNO.FileStation.CheckPermission": {"write": "3"},
	"SYNO.FileStation.Rename":          {"rename": "2"},
	"SYNO.FileStation.CopyMove":        {"start": "3", "status": "3", "stop": "3"},
	"SYNO.FileStation.Delete":          {"start": "2", "status": "2", "stop": "2"},
}

// callAsUser runs one Web API call as runner, the delegated DSM user, so DSM
// applies that user's own permissions. It is the only path for user calls.
func (d *liveDSM) callAsUser(ctx context.Context, runner, api, method, version string, params map[string]any) (json.RawMessage, error) {
	b, err := d.execAsUser(ctx, runner, api, method, version, params)
	if err != nil {
		return nil, err
	}
	return decodeEnvelope(b)
}

// execAsUser is callAsUser's allowlisted command, answering the raw reply.
func (d *liveDSM) execAsUser(ctx context.Context, runner, api, method, version string, params map[string]any) ([]byte, error) {
	if runner == "" {
		return nil, errors.New("runner")
	}
	if allowed, ok := userCalls[api][method]; !ok || allowed != version {
		return nil, errors.New("call not allowlisted")
	}
	return d.exec(ctx, api, method, version, runner, params)
}
func (d *liveDSM) API(ctx context.Context, runner, op string, p map[string]any) (json.RawMessage, error) {
	api, method := "", ""
	version := "2"
	params := map[string]any{}
	switch op {
	case "files.list":
		api = "SYNO.FileStation.List"
		method = "list"
		params["folder_path"] = p["path"]
		params["additional"] = []string{"size"}
	case "files.info":
		api = "SYNO.FileStation.List"
		method = "getinfo"
		params["path"] = []any{p["path"]}
		params["additional"] = []string{"size"}
	case "files.createFolder":
		api = "SYNO.FileStation.CreateFolder"
		method = "create"
		params["folder_path"] = p["path"]
		params["name"] = p["name"]
		params["force_parent"] = p["parents"] == true
	case "files.rename":
		api = "SYNO.FileStation.Rename"
		method = "rename"
		params["path"] = []any{p["path"]}
		params["name"] = []any{p["name"]}
	case "files.checkPermission":
		api = "SYNO.FileStation.CheckPermission"
		method = "write"
		version = "3"
		params["path"] = p["path"]
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return nil, err
		}
		// A fresh name each time: an existing file of that name makes DSM skip.
		params["filename"] = ".synopkgland-permission-probe-" + hex.EncodeToString(random)
		params["overwrite"] = false
		params["size"] = 0
	default:
		return nil, errors.New("operation")
	}
	data, err := d.callAsUser(ctx, runner, api, method, version, params)
	if err != nil {
		return nil, err
	}
	// Some File Station methods report per-file errors inside success:true.
	if hasDSMError(data) {
		return nil, &dsmError{Code: embeddedCode(data)}
	}
	var shape map[string]json.RawMessage
	if json.Unmarshal(data, &shape) != nil {
		return nil, errors.New("DSM data")
	}
	if op == "files.checkPermission" {
		var skip *bool
		if json.Unmarshal(shape["blSkip"], &skip) != nil || skip == nil || *skip {
			return nil, errors.New("permission not confirmed")
		}
	} else {
		key := "files"
		if op == "files.createFolder" {
			key = "folders"
		}
		var entries []json.RawMessage
		if json.Unmarshal(shape[key], &entries) != nil || entries == nil {
			return nil, errors.New("missing files")
		}
		if (op == "files.info" || op == "files.createFolder" || op == "files.rename") && len(entries) != 1 {
			return nil, errors.New("missing target")
		}
	}
	return data, nil
}

// embeddedCode is the code of the first error File Station reports inside
// data, looking through objects in key order and arrays in order.
func embeddedCode(data json.RawMessage) int {
	var v any
	json.Unmarshal(data, &v)
	var walk func(any) int
	walk = func(v any) int {
		switch x := v.(type) {
		case map[string]any:
			if refused, ok := x["error"].(map[string]any); ok {
				if code := refusedCode(refused); code != 0 {
					return code
				}
			}
			if code := listedCode(x["errors"]); code != 0 {
				return code
			}
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if code := walk(x[k]); code != 0 {
					return code
				}
			}
		case []any:
			for _, v := range x {
				if code := walk(v); code != 0 {
					return code
				}
			}
		}
		return 0
	}
	return walk(v)
}

// errorCode is the code of one DSM error object.
func errorCode(raw json.RawMessage) int {
	var refused map[string]any
	if json.Unmarshal(raw, &refused) != nil {
		return 0
	}
	return refusedCode(refused)
}

// refusedCode prefers the cause in errors[] to the summary code: DSM answers
// {"code":1000,"errors":[{"code":1003,…}]} for a copy into a taken name.
func refusedCode(refused map[string]any) int {
	if code := listedCode(refused["errors"]); code != 0 {
		return code
	}
	code, _ := refused["code"].(float64)
	return int(code)
}

// listedCode is the first code in a File Station errors[] list.
func listedCode(v any) int {
	list, _ := v.([]any)
	for _, item := range list {
		if listed, ok := item.(map[string]any); ok {
			if code, ok := listed["code"].(float64); ok && code != 0 {
				return int(code)
			}
		}
	}
	return 0
}

// fileStationCodes maps File Station error codes to the SDK's error codes.
var fileStationCodes = map[int]string{
	105: "PERMISSION_DENIED", 407: "PERMISSION_DENIED", 411: "PERMISSION_DENIED",
	408: "not-found", 412: "name-too-long", 413: "name-too-long", 414: "destination-exists",
	1003: "destination-exists", 1004: "destination-exists",
	415: "NO_SPACE", 416: "NO_SPACE", 418: "invalid-path", 419: "invalid-path",
}

// fileStationCode is the SDK code for a failed File Station call.
func fileStationCode(err error) string {
	var refused *dsmError
	if errors.As(err, &refused) && fileStationCodes[refused.Code] != "" {
		return fileStationCodes[refused.Code]
	}
	return "DSM_FAILED"
}
func hasDSMError(data json.RawMessage) bool {
	var v any
	if json.Unmarshal(data, &v) != nil {
		return true
	}
	var walk func(any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if k == "error" || (k == "errors" && listedCode(v) != 0) {
					return true
				}
				if k == "success" && v == false {
					return true
				}
				if walk(v) {
					return true
				}
			}
		case []any:
			for _, v := range x {
				if walk(v) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}
