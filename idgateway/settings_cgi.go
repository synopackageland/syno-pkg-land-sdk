package idgateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/synopackageland/syno-pkg-land-sdk/installsettings"
)

// SettingsCGI manages external origins on the DSM origin only. GET is an
// unprivileged token bootstrap; every POST authenticates again before reading state.
type SettingsCGI struct {
	Config     Config
	Verifier   SessionVerifier
	Membership GroupMembership
	Projection func() ExternalProjection
}

func (h *SettingsCGI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	locale, messages := denialMessages(r)
	w.Header().Set("Content-Language", locale)
	w.Header().Add("Vary", "Accept-Language")
	fail := func(status int, key string) { http.Error(w, messages["external_"+key], status) }
	if !validRequestAuthority(r) {
		fail(400, "invalid")
		return
	}
	if r.Method == http.MethodGet {
		nonce := randomID(16)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; script-src 'nonce-%s'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'", nonce))
		// No config, hostname, saved origin or projection is interpolated here.
		fmt.Fprintf(w, `<!doctype html><html lang="%s"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><form id="f" method="post" action="?"><input type="hidden" name="SynoToken" id="t"></form><script nonce="%s">fetch('/webman/login.cgi?enable_syno_token=yes',{credentials:'same-origin'}).then(function(r){if(!r.ok)throw new Error();return r.json()}).then(function(j){if(!j.SynoToken)throw new Error();document.getElementById('t').value=j.SynoToken;document.getElementById('f').submit()}).catch(function(){document.body.append(document.createTextNode(%q))})</script>`, locale, template.HTMLEscapeString(messages["external_title"]), nonce, messages["external_signin"])
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		fail(405, "invalid")
		return
	}
	if r.Header.Get("Origin") != requestOrigin(r) {
		fail(403, "denied")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if r.ParseForm() != nil || len(r.PostForm["SynoToken"]) != 1 || r.PostForm.Get("SynoToken") == "" || h.Verifier == nil {
		fail(403, "denied")
		return
	}
	session := r.Clone(r.Context())
	session.Header.Set("X-SYNO-TOKEN", r.PostForm.Get("SynoToken"))
	id, err := h.Verifier.Authenticate(session)
	if err != nil {
		fail(403, "denied")
		return
	}
	admin, err := administrator(r.Context(), id.User, h.Membership)
	if err != nil || !admin {
		fail(403, "denied")
		return
	}
	projection := loadSettingsProjection()
	if h.Projection != nil {
		projection = h.Projection()
	}
	state, err := LoadExternalState(h.Config)
	if err != nil {
		fail(503, "unavailable")
		return
	}
	if action := r.PostForm.Get("action"); action != "" {
		if action != "save" {
			fail(400, "invalid")
			return
		}
		for _, key := range []string{"action", "revision", "manual_origin", "dsm_origin"} {
			if len(r.PostForm[key]) != 1 {
				fail(400, "invalid")
				return
			}
		}
		manual, dsm := r.PostForm.Get("manual_origin"), r.PostForm.Get("dsm_origin")
		if manual != "" {
			manual, err = installsettings.CanonicalExternalOrigin(manual)
			if err != nil {
				fail(400, "invalid")
				return
			}
		}
		if dsm != "" {
			dsm, err = installsettings.CanonicalExternalOrigin(dsm)
			if err != nil {
				fail(400, "invalid")
				return
			}
		}
		err = h.save(r, state, projection, manual, dsm)
		if errors.Is(err, errExternalConflict) {
			fail(409, "conflict")
			return
		}
		if errors.Is(err, errExternalInput) {
			fail(400, "invalid")
			return
		}
		if err != nil {
			fail(503, "unavailable")
			return
		}
		state, err = LoadExternalState(h.Config)
		if err != nil {
			fail(503, "unavailable")
			return
		}
	}
	h.page(w, r, locale, messages, state, projection)
}

var errExternalConflict = errors.New("external settings revision changed")
var errExternalInput = errors.New("invalid external settings")

func (h *SettingsCGI) save(r *http.Request, previous ExternalState, projection ExternalProjection, manual, dsm string) error {
	if err := os.MkdirAll(h.Config.StateDir, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(h.Config.StateDir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return errExternalInput
	}
	if owner, ok := st.Sys().(*syscall.Stat_t); !ok || owner.Uid != uint32(os.Geteuid()) {
		return errExternalInput
	}
	lock := filepath.Join(h.Config.StateDir, "external.lock")
	if st, err := os.Lstat(lock); err == nil {
		owner, ok := st.Sys().(*syscall.Stat_t)
		if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
			return errExternalInput
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	unlock, err := lockFile(lock)
	if err != nil {
		return err
	}
	defer unlock()
	state, err := LoadExternalState(h.Config)
	if err != nil {
		return err
	}
	if strconv.FormatUint(state.Revision, 10) != r.PostForm.Get("revision") || state.Revision != previous.Revision {
		return errExternalConflict
	}
	if state.Revision >= 9999999999999999999 {
		return errExternalConflict
	}
	next := ExternalState{Schema: 1, Revision: state.Revision + 1, ManualOrigin: manual, DSMOrigin: dsm}
	effective := EffectiveExternal(h.Config, next, projection)
	dsmOrigin := effective.DSM
	if dsmOrigin == "" {
		dsmOrigin, _ = installsettings.CanonicalExternalOrigin(requestOrigin(r))
	}
	if manual != "" && installsettings.ValidateExternalOrigin(manual, dsmOrigin) != nil {
		return errExternalInput
	}
	if app, ok := projection.Apps[h.Config.Package]; ok && projection.Schema == 1 && (h.Config.External == nil || h.Config.External.Connect != "none") && app.Port == nativePublicPort(h.Config) {
		connect, _ := installsettings.CanonicalExternalOrigin(app.Public.Origin)
		if connect != "" && connect == dsmOrigin {
			return errExternalInput
		}
	}
	if h.Config.External != nil && h.Config.External.OriginChange == "confirm" && EffectiveExternal(h.Config, state, projection).Primary != effective.Primary && r.PostForm.Get("confirm") != "yes" {
		return errExternalInput
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(h.Config.StateDir, ".external-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(h.Config.StateDir, "external.json")); err != nil {
		return err
	}
	dir, err := os.Open(h.Config.StateDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

var externalSettingsPage = template.Must(template.New("external").Parse(`<!doctype html><html lang="{{.Locale}}"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{index .M "external_title"}}</title><h1>{{index .M "external_title"}}</h1><p>{{.Summary}}</p>{{if .Passthrough}}<p>{{index .M "external_passthrough"}} {{.Passthrough}}</p>{{end}}{{if .Optional}}<p>{{index .M "external_optional"}} {{.Optional}}</p>{{end}}<p>{{index .M "external_warning"}}</p><dl><dt>{{index .M "external_lan"}}</dt><dd>{{.LAN}}</dd><dt>{{index .M "external_manual"}}</dt><dd>{{.Effective.Manual}}</dd><dt>{{index .M "external_connect"}}</dt><dd>{{.Effective.Connect}}</dd><dt>{{index .M "external_private"}}</dt><dd>{{index .M "external_private_hint"}}</dd><dt>{{index .M "external_dsm"}}</dt><dd>{{.Effective.DSM}}</dd></dl><form method="post" action="?"><input type="hidden" name="SynoToken" value="{{.Token}}"><input type="hidden" name="action" value="save"><input type="hidden" name="revision" value="{{.Revision}}"><p><label>{{index .M "external_manual"}} <input type="url" name="manual_origin" maxlength="2048" value="{{.State.ManualOrigin}}"></label></p><p><label>{{index .M "external_dsm"}} <input type="url" name="dsm_origin" maxlength="2048" value="{{.State.DSMOrigin}}"></label></p><p>{{index .M "external_format"}}</p>{{if .Confirm}}<p><label><input type="checkbox" name="confirm" value="yes" required>{{index .M "external_confirm"}}</label></p>{{end}}<p>{{index .M "external_restart"}}</p><button type="submit">{{index .M "external_save"}}</button></form></html>`))

func (h *SettingsCGI) page(w http.ResponseWriter, r *http.Request, locale string, m map[string]string, state ExternalState, projection ExternalProjection) {
	summary := m["external_protected"]
	if h.Config.UpstreamAccess == "public" {
		summary = m["external_upstream"]
	}
	optional := make([]string, 0, len(h.Config.OptionalPassthrough))
	for path := range h.Config.OptionalPassthrough {
		optional = append(optional, path)
	}
	slices.Sort(optional)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	scheme := "https://"
	if !h.Config.NativeIngress && h.Config.Listen.HTTPS == 0 {
		scheme = "http://"
	}
	lan := scheme + net.JoinHostPort(hostname(r.Host), strconv.Itoa(nativePublicPort(h.Config)))
	_ = externalSettingsPage.Execute(w, struct {
		Locale, Token, Revision, LAN, Summary, Passthrough, Optional string
		M                                                            map[string]string
		State                                                        ExternalState
		Effective                                                    External
		Confirm                                                      bool
	}{locale, r.PostForm.Get("SynoToken"), strconv.FormatUint(state.Revision, 10), lan, summary, strings.Join(h.Config.Passthrough, ", "), strings.Join(optional, ", "), m, state, EffectiveExternal(h.Config, state, projection), h.Config.External != nil && h.Config.External.OriginChange == "confirm"})
}

// Read only the shared projection contract; never inspect Connect private state.
func loadSettingsProjection() ExternalProjection {
	for _, path := range []string{DefaultProjection, FallbackProjection} {
		var projection ExternalProjection
		_, err := readExternalFile(path, false, &projection)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			continue
		}
		if err == nil && projection.valid() {
			return projection
		}
		return ExternalProjection{}
	}
	return ExternalProjection{}
}
