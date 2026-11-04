# Request-only external credentials

The product promise is **the SDK does not persist sensitive information declared
through this contract**. It is not a promise that third-party App code cannot
read a credential. The App needs the plaintext briefly to authenticate its
immediate SSH, database or external API connection. An App that explicitly
unwraps, copies, logs or stores that plaintext takes it outside this contract.
This is not a sandbox or a memory-erasure guarantee.

## Existing behavior and changes

| Boundary | Existing behavior | Current contract |
| --- | --- | --- |
| Go JSON params | App decodes raw JSON | Declare credential fields with `api.WithRequestSecrets`; decode them as `secret.Value` |
| Frontend params | Ordinary JSON serialization | `requestSecret` exposes plaintext only to `createClient`'s request encoder; ordinary JSON, strings and inspection stay redacted |
| Error diagnostics | Original errors, panic payloads or tracebacks could reach diagnostic sinks | Go API, bridge and background errors, Node handler failures, Python exceptions and Rust handler panics emit redacted events |
| Request observer | Callback received the original request | Callback receives detached request metadata, without body, query, forms or credential header values |
| API error details | Arbitrary public JSON | Complete public details, results and errors pass through unchanged, including declared routes |
| Persistent background work | Inputs, checkpoints, schedules and results can be stored for replay | A credential-bearing declared route cannot access the background store in its request scope, before filesystem access |
| Transport | Guarded same-origin SDK client | Existing transport policy stays in place; generated DSM Window RPC applies the same redirect, credential and cache restrictions |

## Frontend

Use the guarded client for the credential submission, including inside a DSM
Window. The generated `ctx.call` has its own lifecycle retries and is not the
request-only Secret encoder. Do not unwrap a credential to feed it to that retry
path. Use the SDK client's endpoint and current token from the host context:

```ts
import {createClient, requestSecret} from '@synopackageland/sdk'

const client = createClient({
  endpoint: ctx.files.endpoint,
  token: ctx.files.token,
  requestTimeout: ctx.requestTimeout,
})
const credential = requestSecret(passwordInput.value)
passwordInput.value = ''
try {
  await client.call('service.connect', {credential})
} finally {
  credential.clear()
}
```

Require an available host endpoint before enabling this form. A request containing a Secret sends
once, including when optional session read recovery would otherwise retry it. Encoding consumes the wrapper before the
network attempt, including a failed or timed-out attempt. A later explicit retry
requires a fresh credential. A request canceled before encoding sends nothing;
clear the wrapper when abandoning the form. The wrapper's `toJSON`, string
conversion and object inspection do not expose its contents. Use
`redactSecrets(value)` for detached JSON diagnostics, never raw request bodies.
Client results, public errors and transport exceptions retain their original
content. The SDK does not apply field-name or credential-value redaction to
responses, including requests using Secret wrappers.

This does not introduce browser-side encryption. HTTP remains available where
it was available before, with existing warnings and HTTPS-specific ceremonies
retaining their existing checks. The browser client still uses same-origin
credentials, refuses redirects and disables caching. These options also apply
to generated DSM Window RPC dispatches and lifecycle retries.

## Go backend

```go
type ConnectParams struct {
    Credential secret.Value `json:"credential"`
}

route := api.WithRequestSecrets(api.Handle("service.connect",
    func(ctx context.Context, caller api.Caller, raw json.RawMessage) (any, error) {
        var params ConnectParams
        if err := json.Unmarshal(raw, &params); err != nil {
            return nil, api.BadRequest("invalid connection parameters")
        }
        defer params.Credential.Clear()
        return connectExternal(ctx, params.Credential.Reveal())
    }), "credential")
```

Declare every custom credential field name. Names apply recursively, including
nested objects and arrays. Common password, OTP, token, secret, authorization,
cookie and DSM token names are included automatically on declared routes.
Results and public error codes, messages and details pass through unchanged,
including credential-like fields and values. Unknown errors and panic payloads
remain fixed internal client errors, as before request-only secrets; their raw
content never reaches the SDK's error log. `secret.Value`
also redacts ordinary JSON/text/Gob serialization, formatting (including debug
verbs), and structured logging. A serialized redaction marker cannot restore a
credential. `Clear` clears the shared byte holder, including copies of the value;
it cannot erase previously revealed strings or the original HTTP request bytes.

The route must be an ordinary JSON handler. Single calls and independent calls
inside an HTTP batch use the same wrapper. Grouped, raw, response and stream
handlers are refused by this constructor because they bypass that result and
error boundary. Authentication and authorization still run before the handler.

Keep the supplied context for all SDK calls. A credential-bearing declared
route's background store access returns `secret.ErrPersistence` before opening
the store: no inputs, idempotency receipts, schedules, progress, checkpoints or
results enter the journal or retry spool. Do not detach the context to bypass
this rule. The SDK notification file recorder also refuses this scope before opening its
event log. Other SDK settings accept their existing constrained configuration
schemas; do not copy revealed values into settings, state, telemetry, audit
records, caches, arbitrary files, or application-managed persistence.

Diagnostic helpers match credential names exactly (case-insensitively):
`password`, `passwd`, `passphrase`, `otp`, `token`, `secret`, `authorization`,
`cookie`, `synotoken`, `sid`, and the `X-SYNO-TOKEN` header. Prefixes and suffixes
do not match: `sidebar`, `tokenCount`, `secretName`, `otpEnabled` and `api_token`
remain intact unless explicitly declared. Use `secret.NewRedactor(raw, fields...).Redact(value)`
or `redactSecrets(value, credentials, fields)` for application-specific diagnostic fields.
Never apply these helpers to client responses.

`secret.Redact(value)` returns a detached JSON-safe diagnostic copy and fails
closed for unsupported values. `api.WithObserver` still reports the authorized
account, method, code, duration and status through `Outcome`, while its request
argument contains metadata only. It strips bodies, query strings, form data and
credential header values. Other header values remain unchanged. An observer
must not capture the original request in a closure or add a raw-body middleware logger.

## Long-lived connections

A request-only credential cannot be recovered after a restart. Ask for it again
when another connection needs it. For unattended background connections, choose
a restricted, revocable service token and an explicitly designed encrypted
storage policy with ownership, key handling and retention controls. This contract
does not provide that encrypted external-credential store. The existing
[Compose runtime secrets](compose.md) facility provisions private
runtime configuration for containers; it is not a general store for user-entered
external passwords. Do not silently convert request credentials into durable
jobs, cache entries, logs or a persistent credential store.

## Origin fallback

When `Sec-Fetch-Site` is absent, `Origin` (otherwise `Referer`) must contain a
valid HTTP(S) authority matching the request host and port or the existing first
forwarded host. Userinfo, opaque/non-HTTP URLs, multiple source headers and invalid
ports are refused. An Origin cannot contain a path, query or fragment; a Referer
can retain its normal path/query. A scheme difference emits a fixed warning and
is allowed, including behind a TLS-terminating proxy. Requests already proven
same-origin by Fetch Metadata retain their existing behavior. Existing GET/HEAD
downloads without origin proof retain their existing behavior.
