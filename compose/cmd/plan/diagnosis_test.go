package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/compose"
)

func TestDiagnosisNeverTurnsAnAdvisoryProbeIntoANativeFailure(t *testing.T) {
	project := t.TempDir()
	native := filepath.Join(t.TempDir(), "native.log")
	os.WriteFile(native, []byte("App hook result\n"), 0600)
	t.Setenv("SYNOPKG_TEMP_LOGFILE", native)
	t.Setenv("SYNOPKG_DSM_LANGUAGE", "cht")
	catalog := map[string]map[string]string{"en": {"registry_check": "Registry check", "unknown": "The cause could not be determined.", "repair": "After fixing the cause, use Package Center \"Repair\". The system will reinstall the package and download missing images."}, "zh-TW": {"registry_check": "登錄站檢查", "error_code": "（錯誤碼：{code}）", "unknown": "無法判斷原因。", "repair": "修正原因後，使用套件中心的「修復」。"}}
	data, _ := json.Marshal(catalog)
	os.WriteFile(filepath.Join(project, "diagnosis-messages.json"), data, 0600)
	var out bytes.Buffer
	if err := runDiagnosis([]string{"--project", project, "--phase", "postinst"}, &out); err != nil {
		t.Fatal(err)
	}
	var diagnoses []compose.ImageDiagnosis
	if err := json.Unmarshal(out.Bytes(), &diagnoses); err != nil || len(diagnoses) != 1 || diagnoses[0].Source != "registry_check" {
		t.Fatalf("%s %v", out.String(), err)
	}
	text, _ := os.ReadFile(native)
	if string(text) != "App hook result\n" {
		t.Fatal(string(text))
	}
}

func TestDiagnosisNeverWritesThroughNativeLogSymlinkAndFallsBackToEnglish(t *testing.T) {
	project := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("preserved"), 0600)
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(outside, link)
	t.Setenv("SYNOPKG_TEMP_LOGFILE", link)
	t.Setenv("SYNOPKG_DSM_LANGUAGE", "unsupported")
	var out bytes.Buffer
	if err := runDiagnosis([]string{"--project", project, "--phase", "preinst"}, &out); err != nil {
		t.Fatal(err)
	}
	text, _ := os.ReadFile(outside)
	if string(text) != "preserved" {
		t.Fatal("followed native logfile symlink")
	}
	if !strings.Contains(out.String(), "COMPOSE_IMAGE_UNKNOWN") {
		t.Fatal(out.String())
	}
}

func TestNativeDiagnosisUsesLocalizedHeadingAndCodeSuffix(t *testing.T) {
	for _, tc := range []struct {
		language, label, code, want string
	}{
		{"cht", "登錄站檢查", "（錯誤碼：{code}）", "登錄站檢查（registry-1.docker.io）：原因。修正後按「修復」。（錯誤碼：COMPOSE_IMAGE_TLS_TRUST）\n"},
		{"enu", "Registry check", "(Error code: {code})", "Registry check (registry-1.docker.io): Cause. Use Repair. (Error code: COMPOSE_IMAGE_TLS_TRUST)\n"},
	} {
		t.Run(tc.language, func(t *testing.T) {
			catalog := map[string]string{"registry_check": tc.label, "error_code": tc.code, "tls_trust": "Cause.", "repair": "Use Repair."}
			if tc.language == "cht" {
				catalog["tls_trust"], catalog["repair"] = "原因。", "修正後按「修復」。"
			}
			d := compose.ImageDiagnosis{Source: "registry_check", RegistryHost: "registry-1.docker.io", Cause: compose.ImageTLSTrust, Code: "COMPOSE_IMAGE_TLS_TRUST"}
			if got := renderNativeDiagnosis(d, catalog, tc.language); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNativeDiagnosisWithoutRegistryUsesEnglishFallback(t *testing.T) {
	catalog := diagnosisCatalog(t.TempDir(), "unsupported")
	d := compose.DiagnoseImageFailure("manifest unknown", "postinst")
	want := "Container Manager: The required image version is unavailable. Check for an App update or contact its publisher. After fixing the cause, use Package Center \"Repair\". The system will reinstall the package and download missing images. (Error code: COMPOSE_IMAGE_NOT_FOUND)\n"
	if got := renderNativeDiagnosis(d, catalog, "unsupported"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNativeDiagnosisJoinsSegmentsWithLocaleSpacing(t *testing.T) {
	straySpace := regexp.MustCompile(`[。）！] `)
	for _, language := range []string{"zh-TW", "zh-CN", "ja", "cht", "chs", "jpn"} {
		t.Run(language, func(t *testing.T) {
			for _, ending := range []string{"。", "）", "！"} {
				catalog := map[string]string{"registry_check": "檢查", "tls_trust": "原因" + ending, "repair": "修正" + ending, "error_code": "（錯誤碼：{code}）"}
				d := compose.ImageDiagnosis{Source: "registry_check", RegistryHost: "registry-1.docker.io", Cause: compose.ImageTLSTrust, Code: "COMPOSE_IMAGE_TLS_TRUST"}
				got := renderNativeDiagnosis(d, catalog, language)
				if straySpace.MatchString(got) || !strings.Contains(got, "原因"+ending+"修正"+ending+"（錯誤碼：") {
					t.Fatalf("unexpected segment spacing: %q", got)
				}
			}
		})
	}
	for _, tc := range []struct{ language, cause, repair, code, want string }{
		{"krn", "원인입니다.", "복구하십시오.", "(오류 코드: {code})", "Container Manager: 원인입니다. 복구하십시오. (오류 코드: COMPOSE_IMAGE_TLS_TRUST)\n"},
		{"fre", "Cause inconnue.", "Utilisez Réparer.", "(Code d’erreur : {code})", "Container Manager: Cause inconnue. Utilisez Réparer. (Code d’erreur : COMPOSE_IMAGE_TLS_TRUST)\n"},
	} {
		t.Run(tc.language, func(t *testing.T) {
			catalog := map[string]string{"container_manager": "Container Manager", "tls_trust": tc.cause, "repair": tc.repair, "error_code": tc.code}
			d := compose.ImageDiagnosis{Source: "container_manager", Cause: compose.ImageTLSTrust, Code: "COMPOSE_IMAGE_TLS_TRUST"}
			if got := renderNativeDiagnosis(d, catalog, tc.language); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
