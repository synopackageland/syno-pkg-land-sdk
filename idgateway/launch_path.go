package idgateway

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
)

// LauncherPathVersion identifies support for a fixed same-origin menu target.
const LauncherPathVersion = 1

func validateLaunchPath(value string) error {
	if value == "" {
		return nil
	}
	invalid := func() error { return errors.New("launch_path must be a bounded local App URL outside /_sso") }
	if len(value) > 2048 || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsRune(value, '\\') || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return invalid()
	}
	u, err := url.Parse(value)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || !cleanPath(u.Path) || strings.HasPrefix(u.Path, "//") || u.Path == "/_sso" || strings.HasPrefix(u.Path, "/_sso/") {
		return invalid()
	}
	decoded, err := url.PathUnescape(value)
	if err != nil || strings.ContainsRune(decoded, '\\') || strings.IndexFunc(decoded, unicode.IsControl) >= 0 {
		return invalid()
	}
	return nil
}
