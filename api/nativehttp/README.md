# Native HTTP metadata v3

This is a metadata-only contract for matching SDK/CLI builds. It does not add an
HTTP executor, credential store, business routes, listener, or service to the NAS.
Native execution and all credential ceremonies belong to the CLI. Existing API
v1/v2 RPC validation and routes retain their behavior.

The authoritative JSON Schema 2020-12 files are in `schema/`: `registry.schema.json`,
`descriptor.schema.json` and `inventory.schema.json`. Load all three resources
locally; their HTTPS `$id` values are identifiers, not network dependencies.
`ValidateShape` checks structure. `ParseRegistry` additionally checks semantics
against resolved `BuildConfig`; schema validation alone is insufficient.
`testdata/cases.json` defines shared valid/invalid conformance cases and context.
The CLI must consume these same schemas and fixtures, including semantic rejects.

Use `ctl.transport: native-http` explicitly with either Compose or a native
backend. Legacy Compose ctl remains unsupported. `operations` is the sole command
source; every command has an HTTP binding. No Go business handler is required.
Auth is the closed `header` or `greader-client-login` union. Only static ASCII
`Accept`/`Content-Type` negotiation headers are permitted. Credentials never
appear in this contract; enrollment instructions and routes are public metadata.

Paths are fixed App-relative paths. No origin, port, query, percent escapes,
backslash, repeated slash, dot segment, script or expression is accepted in API
paths. Placeholders occupy a whole segment and reference required scalar params.
Auth paths cannot contain placeholders. The registry basePath must equal the
resolved gateway ingressPath byte-for-byte. Every API and auth path must be
segment-covered by a declared upstream_auth_paths prefix; `/api` covers `/api/x`
but not `/apix`. The credential browser page is a relative path with optional
fixed query, never an origin or callback. It need not be an upstream auth path.

Params/result schemas use JSON Schema 2020-12 with local `#` references only.
Params are closed objects. Param bindings must refer to declared properties;
query/form arrays contain scalar items; JSON bodies may bind complex values.
Local file/output params are required writeOnly strings and cannot be sent in
query/body/path. Optional params are omitted when absent, explicit null requires
a nullable schema, arrays repeat query/form keys. Static binding keys cannot
inject credential/identity headers or URL components. Action tokens may appear
only as form `T: {actionToken:true}` under greader auth.

Read/preview AI classes require read effect and GET. Drafts reference an existing
nonmutating preview command. Admin-only entry requires administrator permissions
throughout the registry. Upload/download are stream, human-only, with finite
byte/MIME caps; raw upload has no multipart part or form body. Pagination is GET,
JSON, rpc, with distinct runtime-owned page/cursor and size params. NDJSON is a
bounded text stream. Client runtime rules (no redirect, no mutation replay,
response validation and incomplete pagination) remain mandatory CLI work.

`Export` produces the descriptor and HTTP inventory from the same parsed registry
for either backend. `ValidateArtifacts` checks schema, semantics, fixed CGI path,
package identity, capabilities, every digest and exact inventory/command equality.
Gateway policy hashing reuses `idgateway.Config.EntryPolicyDigest()` on the
**unscoped** gateway config, including empty collection normalization and fixed
Go struct field order. This is the existing gateway contract, not JCS.

All other digests use RFC 8785 JCS UTF-8 JSON and SHA-256 lowercase hex:

- registrySha256: complete v3 registry
- authSha256: `{basePath,auth,entryPolicyDigest}`
- inventorySha256: complete exported inventory
- descriptor sha256: `{format,version,payload}`, excluding top-level sha256

Hash the original JSON values, retaining explicit empty optional objects. Never
hash reserialized typed structs that may omit explicit empty fields. Invalid
Unicode, duplicate keys, nonfinite numbers and nesting beyond 32 are refused.
`testdata/canonical-vectors.json` includes ECMAScript numbers, Unicode, UTF-16 key
ordering and independent expected hashes. Hashes identify content; they are not
signatures. CLI storage keys must separately bind DSM origin/profile/account and
package identity. DSM logout/revoke does not revoke upstream credentials.

The fixed describe request is POST `/webman/3rdparty/<package>/cgi/api.cgi` with
`{"method":"synopkgland.describe","params":{}}`, DSM cookie and SynoToken.
`NewDescriptionHandler` validates and snapshots private packaged descriptor and
inventory files, then reuses SDK DSM/session/origin/entry guards. Unknown methods,
query dispatch, batches and native credential input are refused. App-privilege
requires an existing DSM EntryAuthenticator with the exact matching mode;
admin-only additionally requires a verified administrator. No native request runs.
