package idgateway

import "errors"

func validateEntry(entry, access, upstream string, passthrough, optional int) error {
	switch entry {
	case "":
		return nil
	case "admin-only", "app-privilege":
	default:
		return errors.New("invalid App entry mode")
	}
	if access == "public" || upstream == "public" || passthrough != 0 || optional != 0 {
		return errors.New("App entry cannot combine with public access or passthrough")
	}
	return nil
}

// ProviderOnlyOIDCVersion identifies support for upstream_access in build tools.
const ProviderOnlyOIDCVersion = 1

// validateUpstreamAccess keeps issuer admission independent from App admission.
// The empty value preserves existing packages. Explicit public upstream access
// never turns the issuer itself public.
func validateUpstreamAccess(mode Mode, access, upstream string, blocked, optional bool) error {
	if upstream == "" || upstream == "protected" {
		return nil
	}
	if upstream != "public" {
		return errors.New("upstream_access must be protected or public")
	}
	if mode != ModeOIDC || (access != "dsm-users" && access != "dsm-admins" && access != "dsm-groups") {
		return errors.New("public upstream_access requires oidc and explicit DSM issuer access")
	}
	if blocked || optional {
		return errors.New("public upstream_access cannot bypass Share activation or combine with optional passthrough")
	}
	return nil
}

// IssuerEntry applies DSM Application Privileges even when the App has no
// backend entry declaration. An explicit administrator floor remains stricter.
func (c Config) IssuerEntry() string {
	if c.UpstreamAccess == "public" && c.Entry == "" {
		return "app-privilege"
	}
	return c.Entry
}

func (f *Front) providerOnly() bool { return f.cfg.UpstreamAccess == "public" }
