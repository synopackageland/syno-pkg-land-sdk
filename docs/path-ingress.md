# Identity gateway on a DSM path

`idgateway.PathIngressVersion = 1` identifies support for a gateway served at
`/synopkgland/<package>/`. The CLI generates `native_ingress: true` and
`ingress_path` together. The gateway opens only its private mutually
authenticated Unix socket; the OIDC issuer, when configured, remains on loopback.
The CLI registers the location through DSM's resource worker. Older
`native_ingress` configurations without `ingress_path` retain dedicated-port
behavior.

The nginx location preserves the full request URI. Upstreams must support their
configured base path. Gateway declarations keep paths relative to the package:
policy prefixes, redirects, OIDC client callback paths, browser helpers and the
launcher are scoped once. Sessions and upstream cookies stay under the package
path. DSM cookies and other gateway cookies are stripped before proxying.

Login authorization permits only a return URL on the CGI's own DSM origin and
this package's exact callback path. Cold-login resume uses a transaction-bound
path token rather than choosing an App port. The nginx hop supplies the original
scheme and peer address over mutual TLS; client forwarding headers never grant
identity. Legacy dedicated-port external-origin routing is not used in this mode.

The CLI owns the public manifest and capability check; see its
[ingress guide](https://github.com/synopackageland/syno-pkg-land-cli/blob/main/docs/path-ingress.md).
Do not construct an nginx rule or edit installed gateway JSON in an application.
An upstream without subpath support cannot be made prefix-aware merely by
changing its gateway configuration.
