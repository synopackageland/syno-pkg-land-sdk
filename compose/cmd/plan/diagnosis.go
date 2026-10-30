package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/synopackageland/syno-pkg-land-sdk/compose"
)

// English remains a usable fallback for a missing/older payload catalog.
var diagnosisEnglish = map[string]string{
	"registry_check":    "Registry check",
	"container_manager": "Container Manager",
	"tls_trust":         "The registry certificate could not be verified. Check the NAS date and time. Ask your network administrator to configure trusted certificates for Container Manager. Alternatively, ask them to let this registry bypass network certificate checks.",
	"unreachable":       "Cannot reach the registry. Check the NAS network, firewall and Container Manager proxy settings.",
	"dns":               "The registry name could not be resolved. Check the NAS DNS settings.",
	"rate_limit":        "The registry download limit was reached. Wait before trying again. You can also ask an administrator to check registry sign-in.",
	"disk_full":         "There is not enough storage for the image. Free space on Container Manager's storage volume.",
	"not_found":         "The required image version is unavailable. Check for an App update or contact its publisher.",
	"denied":            "The registry denied access. Ask an administrator to check registry sign-in and image permissions.",
	"unknown":           "The check could not determine the cause. Open Container Manager for details or contact the App publisher.",
	"repair":            "After fixing the cause, use Package Center \"Repair\". The system will reinstall the package and download missing images.",
	"error_code":        "(Error code: {code})",
}

// runDiagnosis always treats an observed check failure as information. Only an
// invalid invocation is an error; generated hooks also preserve their own exit.
func runDiagnosis(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("diagnose", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	project := fs.String("project", "", "packaged Compose directory")
	phase := fs.String("phase", "", "native hook phase")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *project == "" {
		return errors.New("diagnose requires a project and phase")
	}
	switch *phase {
	case "preinst", "postinst", "postupgrade", "prestart":
	default:
		return errors.New("invalid diagnosis phase")
	}
	results := compose.CheckRegistries(context.Background(), *project, *phase)
	if err := json.NewEncoder(out).Encode(results); err != nil {
		return err
	}
	// A /v2/ probe is advisory, not a native pull result. Container Manager
	// may use a mirror, cached image or a different trust store. Its operation
	// owns the package result log; a probe must never append Repair advice.

	return nil
}

// renderNativeDiagnosis keeps the recovery action before the final support code.
func renderNativeDiagnosis(d compose.ImageDiagnosis, catalog map[string]string, language string) string {
	label := catalog[d.Source]
	if label == "" {
		label = catalog["registry_check"]
	}
	open, close, colon, separator := " (", ")", ": ", " "
	switch diagnosisLanguage(language) {
	case "zh-TW", "zh-CN", "ja":
		open, close, colon, separator = "（", "）", "：", ""
	}
	if d.RegistryHost != "" {
		label += open + d.RegistryHost + close
	}
	code := strings.ReplaceAll(catalog["error_code"], "{code}", d.Code)
	return label + colon + catalog[string(d.Cause)] + separator + catalog["repair"] + separator + code + "\n"
}

func diagnosisLanguage(language string) string {
	aliases := map[string]string{"enu": "en", "cht": "zh-TW", "chs": "zh-CN", "jpn": "ja", "krn": "ko", "ger": "de", "fre": "fr", "spn": "es", "ita": "it"}
	if alias := aliases[language]; alias != "" {
		language = alias
	}
	return language
}

func diagnosisCatalog(project, language string) map[string]string {
	language = diagnosisLanguage(language)
	result := map[string]string{}
	for k, v := range diagnosisEnglish {
		result[k] = v
	}
	f, err := os.OpenFile(filepath.Join(project, "diagnosis-messages.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return result
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 256<<10 {
		return result
	}
	var catalog map[string]map[string]string
	if json.NewDecoder(io.LimitReader(f, 256<<10)).Decode(&catalog) != nil {
		return result
	}
	for _, locale := range []string{"en", language} {
		for key := range result {
			text := catalog[locale][key]
			// Catalogs are packaged text, not formatting programs or native log markup.
			if text != "" && len(text) <= 2048 && !strings.ContainsAny(text, "\r\n\x00") {
				result[key] = text
			}
		}
	}
	return result
}

func appendNativeDiagnosis(path, message string) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size()+int64(len(message)) > 64<<10 {
		return
	}
	// Ignore a display-channel failure; it must not mask the native hook result.
	_, _ = io.WriteString(f, message)
}
