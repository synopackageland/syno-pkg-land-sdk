package idgateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func externalInputs() (Config, ExternalState, ExternalProjection) {
	cfg := Config{Package: "App", Listen: Listen{HTTPS: 18443}, PublicOrigin: "https://local.example.com:18443"}
	state := ExternalState{Schema: 1, Revision: 9, ManualOrigin: "https://manual.example.com:443", DSMOrigin: "https://dsm.example.com"}
	projection := ExternalProjection{Schema: 1, DSM: ExternalProjectionDSM{Origin: "https://connect-dsm.example.com"}, Apps: map[string]ExternalProjectionApp{
		"App": {Port: 18443, Rev: 17, Public: ExternalProjectionPublic{Origin: "https://connect.example.com"}},
	}}
	return cfg, state, projection
}

func TestEffectiveExternal(t *testing.T) {
	cfg, state, projection := externalInputs()
	got := EffectiveExternal(cfg, state, projection)
	if !reflect.DeepEqual(got.Accept, []string{"https://manual.example.com", "https://connect.example.com"}) || got.Primary != got.Accept[0] || got.DSM != state.DSMOrigin || got.Revision() != "9.17" {
		t.Fatal(got)
	}
	if got.Index("https://MANUAL.example.com:443") != 0 || got.Index("https://manual.example.com:444") != -1 {
		t.Fatal("canonical lookup")
	}
	state.ManualOrigin = projection.Apps["App"].Public.Origin
	if got := EffectiveExternal(cfg, state, projection); len(got.Accept) != 1 {
		t.Fatal("duplicate", got)
	}
	state.ManualOrigin = ""
	state.DSMOrigin = ""
	if got := EffectiveExternal(cfg, state, projection); got.Primary != "https://connect.example.com" || got.DSM != projection.DSM.Origin {
		t.Fatal("fallback", got)
	}
	cfg.External = &ExternalConfig{Connect: "none"}
	if got := EffectiveExternal(cfg, state, projection); len(got.Accept) != 0 || got.Primary != cfg.PublicOrigin || got.R2 != 0 || got.DSM != "" {
		t.Fatal("none", got)
	}
	state.ManualOrigin = "https://manual.example.com"
	if got := EffectiveExternal(cfg, state, projection); got.Primary != state.ManualOrigin {
		t.Fatal("manual with none", got)
	}
}

func TestExternalInvalidInputsAndOriginSeparation(t *testing.T) {
	for _, edit := range []func(*Config, *ExternalState, *ExternalProjection){
		func(c *Config, s *ExternalState, p *ExternalProjection) { p.Schema = 2 },
		func(c *Config, s *ExternalState, p *ExternalProjection) {
			p.Apps["App"] = ExternalProjectionApp{Port: 9999, Rev: 17, Public: ExternalProjectionPublic{Origin: "https://connect.example.com"}}
		},
		func(c *Config, s *ExternalState, p *ExternalProjection) {
			p.Apps["App"] = ExternalProjectionApp{Port: 18443, Rev: 17, Public: ExternalProjectionPublic{Origin: "http://connect.example.com"}}
		},
	} {
		cfg, state, p := externalInputs()
		edit(&cfg, &state, &p)
		if got := EffectiveExternal(cfg, state, p); got.Connect != "" || got.R2 != 0 || got.Manual == "" {
			t.Fatal(got)
		}
	}
	cfg, state, p := externalInputs()
	state.ManualOrigin = "https://dsm.example.com:443"
	if got := EffectiveExternal(cfg, state, p); got.Manual != "" || got.Primary != got.Connect {
		t.Fatal("same DSM origin", got)
	}
	state.ManualOrigin = "https://manual.example.com"
	p.DSM.Origin = state.ManualOrigin
	state.DSMOrigin = ""
	if got := EffectiveExternal(cfg, state, p); got.Manual != "" {
		t.Fatal("projection changed DSM", got)
	}
	state.Revision = ^uint64(0)
	if got := EffectiveExternal(cfg, state, p); got.R1 != 0 || got.Manual != "" {
		t.Fatal("invalid state", got)
	}
}

func TestExternalConfigStrictLoad(t *testing.T) {
	for _, body := range []string{`{}`, `{"connect":"none"}`, `{"connect":"optional","origin_change":"confirm"}`} {
		path, _ := writeConfig(t, strings.Replace(paperlessConfig, `"mode": "oidc",`, `"mode": "oidc", "external":`+body+`,`, 1))
		if _, err := LoadConfig(path); err != nil {
			t.Fatal(body, err)
		}
	}
	for _, body := range []string{`{"connect":"required"}`, `{"origin_change":"auto"}`, `{"projection":"/tmp/x"}`, `{"state_file":"/tmp/x"}`} {
		path, _ := writeConfig(t, strings.Replace(paperlessConfig, `"mode": "oidc",`, `"mode": "oidc", "external":`+body+`,`, 1))
		if _, err := LoadConfig(path); err == nil {
			t.Fatal("accepted", body)
		}
	}
}

func writeExternalJSON(t *testing.T, path string, v any, mode os.FileMode) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, mode); err != nil {
		t.Fatal(err)
	}
}
func externalReaderFixture(t *testing.T) (*ExternalReader, *time.Time, string) {
	t.Helper()
	cfg, st, p := externalInputs()
	cfg.StateDir = t.TempDir()
	path := filepath.Join(t.TempDir(), "external.json")
	writeExternalJSON(t, cfg.ExternalStatePath(), st, 0600)
	writeExternalJSON(t, path, p, 0644)
	r := NewExternalReader(cfg)
	r.paths = []string{path}
	now := time.Unix(1700000000, 0)
	r.now = func() time.Time { return now }
	r.infof = func(string, ...any) {}
	return r, &now, path
}
func TestExternalReaderPollingAndAtomicReplacement(t *testing.T) {
	r, now, path := externalReaderFixture(t)
	if got := r.Read(); got.R1 != 9 || got.R2 != 17 {
		t.Fatal(got)
	}
	_, state, _ := externalInputs()
	state.Revision = 10
	state.ManualOrigin = "https://new.example.com"
	tmp := r.cfg.ExternalStatePath() + ".tmp"
	writeExternalJSON(t, tmp, state, 0600)
	if err := os.Rename(tmp, r.cfg.ExternalStatePath()); err != nil {
		t.Fatal(err)
	}
	if got := r.Read(); got.R1 != 9 {
		t.Fatal("read before polling", got)
	}
	*now = now.Add(5 * time.Second)
	if got := r.Read(); got.R1 != 10 || got.Primary != state.ManualOrigin {
		t.Fatal(got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Second)
	if got := r.Read(); got.Connect != "" || got.Manual != state.ManualOrigin {
		t.Fatal("removed Connect affected manual", got)
	}
}
func TestExternalReaderRejectsUnsafeFilesAndThrottles(t *testing.T) {
	for _, kind := range []string{"mode", "symlink", "hardlink", "parent-mode", "parent-link", "corrupt", "unknown", "trailing", "future", "large"} {
		t.Run(kind, func(t *testing.T) {
			r, now, path := externalReaderFixture(t)
			n := 0
			r.infof = func(string, ...any) { n++ }
			switch kind {
			case "mode":
				os.Chmod(path, 0664)
			case "symlink":
				os.Rename(path, path+".real")
				os.Symlink(path+".real", path)
			case "hardlink":
				os.Link(path, path+".link")
			case "parent-mode":
				os.Chmod(filepath.Dir(path), 0777)
			case "parent-link":
				os.Symlink(filepath.Dir(path), filepath.Dir(path)+"-link")
				t.Cleanup(func() { os.Remove(filepath.Dir(path) + "-link") })
				r.paths = []string{filepath.Join(filepath.Dir(path)+"-link", "external.json")}
			case "corrupt":
				os.WriteFile(path, []byte(`{`), 0644)
			case "unknown":
				os.WriteFile(path, []byte(`{"schema":1,"secret":true}`), 0644)
			case "trailing":
				os.WriteFile(path, []byte(`{"schema":1} {}`), 0644)
			case "future":
				os.WriteFile(path, []byte(`{"schema":2}`), 0644)
			case "large":
				os.WriteFile(path, []byte(strings.Repeat(" ", 1<<20+1)), 0644)
			}
			for i := 0; i < 3; i++ {
				got := r.Read()
				if got.Connect != "" || got.Manual == "" {
					t.Fatal(got)
				}
				*now = now.Add(5 * time.Second)
			}
			if n != 1 {
				t.Fatal("INFO throttle", n)
			}
		})
	}
}
func TestExternalStateSecurity(t *testing.T) {
	for _, mode := range []os.FileMode{0644, 0660, 0400} {
		r, _, _ := externalReaderFixture(t)
		os.Chmod(r.cfg.ExternalStatePath(), mode)
		if s, err := LoadExternalState(r.cfg); err == nil || s.ManualOrigin != "" {
			t.Fatal("unsafe state", mode, s, err)
		}
	}
	cfg := Config{StateDir: t.TempDir()}
	if s, err := LoadExternalState(cfg); err != nil || s.Schema != 1 || s.Revision != 0 {
		t.Fatal(s, err)
	}
}

func FuzzEffectiveExternal(f *testing.F) {
	f.Add(`{"schema":1,"revision":1,"manual_origin":"https://app.example.com","dsm_origin":"https://dsm.example.com"}`, `{"schema":1,"apps":{}}`)
	f.Add(`{`, `{}`)
	f.Fuzz(func(t *testing.T, rawState, rawProjection string) {
		if len(rawState) > 4096 || len(rawProjection) > 65536 {
			t.Skip()
		}
		var s ExternalState
		var p ExternalProjection
		json.Unmarshal([]byte(rawState), &s)
		json.Unmarshal([]byte(rawProjection), &p)
		got := EffectiveExternal(Config{Package: "App", Listen: Listen{HTTPS: 18443}}, s, p)
		if len(got.Accept) > 2 {
			t.Fatal(got)
		}
		for _, o := range got.Accept {
			if o == got.DSM || got.Index(o) < 0 {
				t.Fatal(got)
			}
		}
	})
}
