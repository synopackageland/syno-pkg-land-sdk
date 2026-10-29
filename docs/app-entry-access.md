# App entry enforcement and access-path audit

`entry: admin-only` requires a verified DSM administrator before App code runs.
`entry: app-privilege` requires a verified session and DSM Application Privileges
admission (administrators are admitted). Route permissions can further restrict
access. A public App route does not override either entry floor.

The CLI rejects public or authenticated App permissions in an administrator-only
App. It also rejects explicit entry combined with public gateway access or
`upstream_access: public`. Permanent/optional passthrough with entry requires
containment in static `identity_gateway.upstream_auth_paths`.
[Upstream-authenticated paths](gateway-access.md#upstream-authenticated-paths)
delegate admission only for the declared upstream prefixes. No-entry Apps retain
public routes with anonymous callers and existing gateway exceptions.

## Packaged policy

The CLI links `api.expectedEntryMode`, `api.expectedPermissionsDigest` and
`api.expectedSDKPermissionsDigest` into the CGI or bridge runtime. Startup and
route description check the App policy bytes against the bound digest and reject
App registrations missing from that policy. SDK methods use the immutable SDK
table. Direct Go development builds without linker bindings retain their
administrator default for unlisted methods.

`dsm.EntryGate.EntryMode` automatically carries the floor into the server.
Packaged administrator-only builds still enforce the floor if a wrapper loses
that interface. Application-privilege builds require a gate-bearing
authenticator; wrappers must forward `EntryMode`. Unknown or mismatched modes
refuse startup.

Gateway binaries also bind `idgateway.expectedEntryPolicyDigest` to the entry
mode, access settings, upstream-auth paths and permanent/optional passthrough.
Configuration loading and direct front construction refuse a mismatched policy.
This gateway declaration never changes API server or SDK method authorization.

`HandleIntegration` routes are the one App-declared exception. Each needs the
`"integration"` permission and a matching `dsm-package.yaml` `integrations`
entry, which the CLI shows on the approval screen. Only those methods skip
the session and entry gate; their handlers verify the caller and use
`api.ActAs`, which runs the entry gate for the named account. See
[HTTP boundaries](http-boundaries.md#integration-callbacks).

The only public ceremony exception consists of `identity.status`,
`identity.login`, `identity.session` and `identity.logout`. These fixed SDK
operations establish, inspect or dispose of an App identity. They do not invoke
business handlers. Their internal SDK-only server uses its own fixed policy.
Only routes carrying the SDK's internal ceremony marker receive this exception;
App handlers using those same names still pass entry admission. The marker key
is issued behind the SDK's Go internal import boundary.

## Dispatch inventory

References identify the current source entry points. Regression evidence is in
`api/entry_paths_test.go`, `dsm/entry_http_test.go`,
`bridge/entry_paths_test.go`, `idgateway/entry_floor_test.go` and
`ts/src/client-entry.test.ts`.

| Path and source | Decision and verdict |
| --- | --- |
| JSON and public App routes: `api/server.go:308` | `authorize` applies entry before route policy; public skips authentication only with no entry or for marked SDK ceremony routes. Closed. |
| All-public, mixed and grouped batch: `api/batch.go:103` | Every call uses the same `authorize`; one lazy cached authentication per request, including failure. Denied calls never enter the group handler. Closed. |
| Request/response handlers and NDJSON: `api/server.go:245` | Dispatch follows central authorization; streaming responses do not create another admission decision. Closed. |
| Upload, download and form adapters: `api/server.go:222` | Transfer preparation normalizes transport and origin before central authorization. It grants no identity. Closed. |
| API description CGI helper: `api/description.go:47` | Applies entry before reading the description. Export validates the bound policy and complete App registry. Closed. |
| Integration callbacks: `api/integration.go` | Only `HandleIntegration` routes with the `integration` permission skip the session and entry gate; selected by one `?method=` value, refused on JSON, batch and transfer paths. The handler verifies the caller; `ActAs` reruns entry admission for the named account and never grants admin. Explicit bounded exception, listed for approval. |
| Standalone identity: `identity/http.go:69` | Ceremony uses a fixed SDK-only server; business JSON and transfers continue into the gated App server. Explicit bounded exception. |
| Python, Node and Rust bridge: `bridge/runtime.go:100`, `bridge/routes.go:17` | One Go entry gate and registry protect JSON, grouped calls and transfers before spawning App code. Closed. |
| Bridge service reentry: `bridge/services.go:126`, `api/registry.go:83` | Verified caller reaches SDK services; direct invocation refuses anonymous or non-admin callers under the bound administrator entry. Closed within the trusted bridge. |
| Gateway HTTP, DAV and initial upgrade: `idgateway/front.go` | Static upstream-auth prefixes delegate admission to upstream auth after path validation and identity stripping. Other entry-protected requests recheck admission before dispatch. |
| Assertion redemption and OIDC authorization: `idgateway/front.go:523` | Rechecks admission between CGI verification and session/code creation. Closed. |
| OIDC token and userinfo: `idgateway/provider.go:285` | Entry-bound providers recheck admission on code redemption and access-token use, using the original browser address for Application Privileges. Closed at provider endpoints. |
| WebSocket continuation: `idgateway/websocket.go:128` | DSM-protected streams periodically recheck account/admission. Upstream-auth streams use upstream credentials and lifetimes. |
| DSM issuer CGI: `idgateway/authorize_cgi.go:130` | Uses the configured verifier and entry gate before signing an assertion. Closed. |
| Ingress, portal and menu launcher: `idgateway/admission.go:129` | Fixed redirects, bootstrap pages and sign-in transport do not execute App handlers. Protected proxy/CGI decisions remain authoritative. Inert launch content is intentionally reachable. |
| Background APIs: `api/server.go:308`, `api/sdk-permissions.json:11` | Generated job/status/result/schedule routes pass central entry first. Worker execution uses the private package spool. |
| Background worker: `background/runner.go:117`, `bridge/cmd/runtime/main.go:84` | Fixed algorithms consume accepted jobs as the non-root package account. Entry changes do not automatically cancel existing jobs or schedules. Separate local trust boundary. |
| Daemon socket: `daemonctl/main.go:80`, `daemonctl/main.go:633` | Requires the configured non-root package account, private state and a mode-0600 Unix socket with peer checks. Lifecycle IPC is a separate local trust boundary. |
| Delegate/executor: `executor/executor.go:331`, `executor/executor.go:414`, `executor/dsm.go:231` | Package/root peer admission precedes fixed operations; delegated user work resolves the account and rechecks the policy's entry. Explicit package lifecycle controls remain local operations. |
| Delegated WebAPI uploads: `delegate/webapi.go:170`, `executor/webapi.go:276` | Private staged upload uses the same delegated account/entry revalidation before DSM dispatch. No new anonymous browser path. |
| App runners: `node/src/app.ts:96`, `python/synopkgland_app/__init__.py:279`, `rust/app/src/lib.rs:363` | Private stdio receives the verified caller from Go; runners refuse root. They do not open a public HTTP server. |
| Browser client: `ts/src/client.ts:27` | Transports server refusals; cannot set caller identity, administrator membership or entry authority. Presentation checks are advisory. |
| Static assets and custom servers | Inert HTML/JS is not privileged App execution. Entry protects SDK APIs and configured gateway traffic. Independent listening ports and foreign servers are outside this contract. |

## CLI registration audit and migration

`check` parses the shipped Linux import graphs of configured Go binaries without running App
initializers or `--describe`. It checks every SDK route constructor against the
App policy or immutable SDK table, understands import aliases and literal
constants, and refuses dynamic method names, constructor aliases, custom
unregistered `ServeHTTP`, direct standard-library HTTP listeners, dot imports and
source symlinks and App-created gateway listeners. Fixed `api.HandleHTTP`
routes and private `ipc.ServeUnix` handler graphs have explicit
[HTTP boundaries](http-boundaries.md); unshipped dev commands are excluded.
Upstream-auth prefixes never exempt SDK registrations. Build repeats the audit after the UI hook so a hook cannot add
an undeclared handler after validation. Bridge method inventories must also be
fully declared.

This is a source registration check, not a whole-program sandbox. External
dependencies, generated executables, arbitrary CGI files and independent
upstream listeners remain trusted packaging inputs. The runtime digest and
registry checks protect SDK dispatch when source inspection cannot resolve an
external helper. This does not prove arbitrary App code cannot expose another
server.

Every App method must appear in the permissions table, including methods whose
permission equals `default: administrators`. Generated native
templates receive explicit policies for `app.status`, `greeting.get` and
`greeting.set`. Custom HTTP dispatch must move into `HandleRequest`,
`HandleResponse`, `HandleStream` or fixed `HandleHTTP`; dynamic registries must declare fixed method
names. Use `upstream_auth_paths` for client API paths that use upstream
authentication. Rebuild with SDK 0.33.0 and CLI 0.35.0 to bind the entry floor and HTTP inventory.

Administrator admission grants App access, never a root UID. The privileged
executor remains an isolated, explicitly approved helper. CGI Application
Privileges retains its existing 30-second cache; gateway admission rechecks
native privileges per request. Existing upstream-issued sessions and offline JWT
validation depend on the upstream and its token lifetime.
