package idgateway

import "errors"

// ValidateNativeHTTPMetadata validates only the static gateway settings needed by
// the client metadata exporter. It does not initialize a gateway or require any
// listener, upstream connection, state directory, or business handler.
func (c Config) ValidateNativeHTTPMetadata() error {
	if c.IngressPath == "" {
		return errors.New("native HTTP requires a resolved path ingress")
	}
	if err := validateIngressPath(c.Package, c.IngressPath); err != nil {
		return err
	}
	if len(c.UpstreamAuthPaths) == 0 {
		return errors.New("native HTTP requires upstream_auth_paths")
	}
	if err := validateUpstreamAuth(c.UpstreamAuthPaths, c.Passthrough, c.OptionalPassthrough); err != nil {
		return err
	}
	if err := validateEntry(c.Entry, c.Access, c.UpstreamAccess, len(c.Passthrough), len(c.OptionalPassthrough), c.UpstreamAuthPaths); err != nil {
		return err
	}
	if c.Access == "public" || c.UpstreamAccess == "public" {
		return errors.New("native HTTP metadata requires DSM entry protection")
	}
	return nil
}
