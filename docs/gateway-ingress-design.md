# DSM-managed HTTPS for gateway Apps

The gateway can use a separate HTTPS origin served by DSM nginx. The CLI
selects this transport by default for gateway SDK 0.24.0 and a package
target of DSM 7.2-64570 or later. There is no per-App ingress option, extra
service dependency, root App process, or executor capability.

DSM owns the public certificate and TLS listener. The package never reads or
copies a DSM private key. The App remains behind the identity gateway, including
its DSM session, current administrator/group admission, origin/CSRF checks and
WebSocket session revalidation.

## Certificate trust

The generated server inherits DSM's **system default certificate**. That
certificate must be trusted by the browser and cover the hostname used to open
the App. Accepting a certificate exception on the DSM desktop does not establish
trust for another origin. A custom DSM-domain or Login Portal certificate
assignment does not automatically change the system default certificate used by
this listener.

DSM handles public certificate loading and renewal. Its nginx reload picks up
the inherited certificate; the gateway stores no copy of it and needs no
restart for a public certificate change. The native worker mechanism was probed
on DSM 7.3.2. The exact DSM 7.2-64570 floor still needs device acceptance.
Long-term unattended renewal and fresh-browser trust are separate acceptance
checks; serving the same certificate fingerprint is not proof of either.

## Private connection and lifecycle

The CLI declares a `web-config/nginx-static-config` server resource and the INFO
nginx reload fields. The existing HTTPS port belongs to nginx; if only an HTTP
port was declared, that port becomes the native HTTPS entry. A separately
declared HTTP port redirects to the HTTPS entry.

nginx proxies to a package-specific Unix socket using mutually authenticated
TLS. After DSM creates the package state directory, the package's post-install
and post-upgrade hooks prepare its private connection keypair using DSM's
OpenSSL. Package start then activates the nginx configuration.
These files stay in the package's private state directory. The nginx configuration
contains file paths, never key material. The nginx master loads its private
upstream key without making the App or package process root.

The socket itself grants no authority. A caller without the package keypair
cannot complete its TLS handshake. Only the authenticated proxy can supply the
client IP used by gateway sessions. nginx overwrites that header, preserves the
external Host and Origin, and forwards upgrades without buffering. Public direct
listeners do not trust the proxy header. Cookies remain host-only, HttpOnly and
Secure on the native entry.

The resource worker owns nginx registration and removal. Stopping the gateway
closes its socket; stopping/removing the package withdraws its public ingress.
Private connection credentials persist with retained App data across upgrades.
Incomplete, expired or readable-by-others credentials fail closed.

## Direct fallback

Older SDK releases or package targets below the native worker floor retain the
existing direct listener. Opening the generated gateway entry first explains
that the direct connection may show a certificate warning or use unencrypted
HTTP, and requires an explicit Continue action. The SDK provides this page in
nine locales; the CLI supplies an English compatibility page for older gateway
binaries.

A configured native worker or private TLS failure is an installation/startup
error. It must not expose an unauthenticated backend or race nginx for the
public port. These failures do not silently switch to a self-signed listener.

## Sources and verification

- [DSM Resource](https://help.synology.com/developer-guide/resource_acquisition/resources.html)
  documents resource acquisition by packages running at reduced privilege.
- [DSM Web Config](https://help.synology.com/developer-guide/resource_acquisition/web_config.html)
  documents server includes, lifecycle ownership and required INFO reload fields.
- [nginx SSL](https://nginx.org/en/docs/http/ngx_http_ssl_module.html)
  documents certificate configuration at the HTTP/server levels.
- [nginx HTTPS upstreams](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_ssl_certificate)
  documents the proxy client certificate and upstream verification settings.

The cross-repository nginx test consumes the CLI's actual generated configuration
and a compiled SDK test binary. It exercises admission, spoofed client headers,
cross-origin mutations, WebSocket data and replacement of the inherited public
certificate while preserving an existing gateway session. It uses disposable
certificates and does not modify a NAS certificate or CA store.
