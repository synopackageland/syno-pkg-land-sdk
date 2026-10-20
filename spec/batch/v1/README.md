# Request batches, version 1

A batch contains independent JSON calls, not a transaction. Responses keep
input order; execution order across an App group and SDK-owned routes is not
specified. Do not batch calls that depend on each other's side effects.

HTTP request to the existing `api.cgi`:

```json
{"v":1,"calls":[{"method":"app.status","params":{}},{"method":"greeting.get"}]}
```

HTTP response (the ordinary outer success/error envelope):

```json
{"success":true,"data":{"v":1,"results":[{"success":true,"data":{"admin":true}},{"success":false,"error":{"code":"admin_required","message":"this action needs the DSM administrators group"}}]}}
```

Omitted params default to `{}`, matching a client single call. Explicit params
keep their JSON value.

The transport guards, body/JSON limits, request deadline and trusted
permissions.json apply unchanged. One to eight methods are allowed. Malformed
batch envelopes fail before dispatch. Each well-formed method is independently
authorized; public methods receive an unauthenticated Caller, and unknown
methods still require authentication. A method error, panic or unencodable Go
result does not discard other results. HTTP stream and response/cookie routes
are refused per method. There are no nested batches, retries or rollback.

The CGI authenticates once per HTTP request. It retains no authentication
result for later requests. App methods share one process, using the explicitly
negotiated bridge v1 batch extension: the runtime adds `batch:1` to hello, and
requires the same in the App's hello before sending a `t:batch,v:1,calls:[...]`
frame. Each call includes its ordinary call frame and its own verified ctx.
The App returns one ordinary result frame per call, with zero-based string
IDs. A service frame carries `batch_id`, which must equal the current call's
index; it acts only for that method's Caller. Old Apps return app_unavailable
for a batch before receiving any calls. Single-call frames are unchanged.

## Executor request scope

The CGI owns the Unix connection; the App never sees credentials or a proof.
For root.run/root.start/root.jobs.* and authenticated status reads inside a batch, the client opens one
`root.batch` transport scope with the normal session carriers:

```json
{"v":1,"op":"root.batch","auth":{"dsm_cookie":"<request only>","dsm_synotoken":"<request only>","remote_addr":"192.0.2.1"},"params":{"v":1,"timeout_ms":60000}}
```

The existing kernel peer gate applies. The executor verifies that request's
session and account once, refuses an unusable policy, and acknowledges
`{"ok":true,"result":{"v":1}}`. Then the same connection carries up to 32
ordinary root operation or authenticated status requests with auth omitted. Credentials on a step,
other operations, malformed frames and unknown versions refuse. The server
inserts the original auth and address internally and dispatches every step
through the existing policy/schema/admin/App Privileges/slot checks. Each
root program still runs independently. Other executor operations retain their
single-operation protocol and checks.

The scope ends at the HTTP deadline, disconnect, or 60 seconds, whichever
comes first. Disconnect cancels an active root.run; already started jobs keep their normal
lifecycle. Nothing is retained for a
later connection/request. The session/account snapshot covers this one bounded
request; App Privileges are queried for every root step. This is not an
executor session or privilege TTL cache. The existing two root.run slots stay.
A new HTTP request always verifies again. A transport failure is never retried,
because preceding steps might have completed.
