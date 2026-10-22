# @synopackageland/app

The Node SDK for the backend of a Synology Package Land App written in
TypeScript or JavaScript. Node 22 or later (DSM Package Center's
`Node.js_v22`), ESM only, no runtime
dependencies.

The package's CGI is the synopkgland Go runtime (`bridge/cmd/runtime`). It
checks the request's origin, the DSM session, the entry gate, the method's
policy in `permissions.json` and the deadline, and only then starts the App,
as the package account, and hands it the call over the bridge channel
([`spec/bridge/v1`](../spec/bridge/v1/README.md)): JSON frames on stdin and
stdout, one process per call. The App never runs as root and never sees the
DSM session; what needs the session it asks of the runtime with
`ctx.runtime`.

## An App

```ts
import fs from 'node:fs'
import path from 'node:path'
import { App, AppError } from '@synopackageland/app'

const app = new App()

app.method('hello.greet', (ctx) => ({ message: `Hello, ${ctx.user}` }))

app.method('hello.save', (ctx, params: { note?: unknown } | null) => {
  if (typeof params?.note !== 'string') throw new AppError('bad_request', 'note must be a string')
  const notes = path.join(process.env.SYNOPKGLAND_DATA_DIR!, 'notes')
  fs.mkdirSync(notes, { recursive: true, mode: 0o700 })
  fs.writeFileSync(path.join(notes, `${ctx.requestId}.txt`), params.note, { flag: 'wx' })
  return { bytes: Buffer.byteLength(params.note) }
})

await app.run()
```

The browser calls it with `@synopackageland/sdk` as any other method:
`{"method":"hello.greet"}` answers `{"success":true,"data":{"message":"Hello, alice"}}`.

The methods must be the ones in the package's `bridge.json` (`methods`);
the runtime registers its routes from there, and an App that says hello with
other methods is refused. Their policy is in `permissions.json`, as for a Go
App.

## API

### `new App(options?)`

- `options.log(error)`: where an unexpected error goes. The default writes it
  to stderr, which DSM keeps in the package's log. The browser only sees
  `internal`.

### `app.method(name, handler)`

Registers a method and returns the App. `name` is `<group>.<name>` in lower
case (`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`); a name registered twice
throws.

`handler(ctx, params)` returns the data, or a promise of it: any JSON value,
`undefined` is `null`. `params` is the request's `params` as the browser sent
it. The type parameter of `method<P>` only annotates it; check the value
before you use it.

### `app.run()`

Answers the one call the runtime sends on stdin, then exits. It refuses to
start as root (exit status 1) and when `SYNOPKGLAND_BRIDGE` is not `stdio`
(exit status 2), so it only runs under the runtime. While it serves, what
the App writes to `process.stdout`, `console.log` included, goes to stderr:
stdout is the channel. A child process started with `stdio: 'inherit'`
would still share it; give it `'ignore'` or pipes.

`node <entry> --describe` prints one JSON line instead,
`{"sdk":"node/…","versions":[1],"methods":[…]}`, so the package build can
check the methods against `bridge.json` and `permissions.json`.
`app.describe()` returns the same object.

### `app.serve(input, output)`

Answers one call on any pair of streams and resolves once the result is
written. `run` uses it on stdin and stdout; tests use it on a
`PassThrough` pair, playing the runtime's side of the channel.

### `Context`

| Member | |
|---|---|
| `user` | The DSM user who called; empty for a public method |
| `admin` | Whether the user is a DSM administrator |
| `locale` | The first `Accept-Language` tag, or empty |
| `requestId` | The request's ID, 32 hex digits, also in the runtime's log |
| `deadline` | When the runtime ends the request, in `Date.now()` milliseconds |
| `deadlineMs` | The time that was left when the runtime sent the call, in milliseconds |
| `signal` | An `AbortSignal` aborted when the request is over; `signal.reason` is `'deadline'` or `'canceled'` |
| `cancelled` | Whether `signal` is aborted |
| `runtime(op, params?)` | Asks the runtime for a service on behalf of the caller; resolves to its result |

All of it comes from the runtime's verified caller. A `Context` belongs to
one call.

### Errors

`throw new AppError(code, message)` answers the browser
`{"success":false,"error":{"code":…,"message":…}}`. `code` is either

- the App's own code, `^[a-z][a-z0-9_]{0,63}$`, except the runtime's
  reserved ones (`session_required`, `admin_required`,
  `app_privilege_denied`, `auth_unavailable`, `unknown_method`,
  `cross_origin`, `origin_required`, `request_timeout`, `request_canceled`,
  `request_too_large`, `json_required`, `method_not_allowed`,
  `app_unavailable`), or
- an upper-case refusal of
  [`spec/protocol/v1/errors.json`](../spec/protocol/v1/errors.json), such as
  `SCOPE_DENIED`, which `@synopackageland/sdk` translates.

Any other code throws `TypeError`, which, like any other error, answers the
browser `internal` and writes why to stderr. `message` is cut to 1024 bytes
of UTF-8. `AppError.badRequest(message)` is `new AppError('bad_request', message)`.

Anything else a handler throws, data JSON cannot encode (a `BigInt`, a
cycle), answers `internal`; the error goes to `log`, never to the browser.

### Services

`ctx.runtime(op, params)` sends the runtime a service request and resolves to
its result, or `null` when it has none. When the runtime refuses it rejects
with `ServiceError` (`op`, `code`, `message`): `unknown_service` for an op
the runtime does not offer, otherwise the service's code. A `ServiceError` a
handler does not catch reaches the browser when its code is a protocol v1
refusal, and is `internal` otherwise. An `op` that is not a service name, or
`params` JSON cannot encode, rejects with `TypeError` before anything is
sent.

The runtime offers the services of the capabilities in `bridge.json`
(`files.*`, `portal.status`, `delegate.*`, `bg.*`, `root.*`), listed with
their params in [`spec/bridge/v1`](../spec/bridge/v1/README.md#services).
They act for the verified caller, with the request's DSM session where they
need one, and end with the call.

```ts
app.method('notes.list', (ctx) => ctx.runtime('files.list', { path: '/home/notes' }))
```

### Transfers

A method `transfers` names in `bridge.json` is an HTTP upload or download.
Its `params` is `{query, upload: {path, size}}` for an upload, a spool file
to read, and `{query, download: {path}}` for a download, a spool file to
write before answering `{name, content_type?}`
([`spec/bridge/v1`](../spec/bridge/v1/README.md#transfers)).

### Deadline and cancel

The runtime owns the deadline. When it passes, or the browser goes away, the
runtime sends `cancel` and `ctx.signal` aborts; one second later it kills the
App's process group. Pass `ctx.signal` to `fetch`, `fs` and timers so that
they stop with the request:

```ts
app.method('feed.fetch', async (ctx) => {
  const reply = await fetch('https://example.com/feed.json', { signal: ctx.signal })
  return reply.json()
})
```

Once `signal` aborts, pending `ctx.runtime` calls, and later ones without
asking, reject with `ServiceError` code `request_canceled`. If the channel
ends early, `signal` aborts with `'canceled'`.

### Numbers

An App gets values, not JSON literals: `2.50` arrives as `2.5`, and integers
above 2^53 lose precision. Carry 64-bit IDs and sizes as strings.

## Testing an App

`serve` takes any streams, so a test can play the runtime:

```ts
import { PassThrough } from 'node:stream'
import { createInterface } from 'node:readline'

const toApp = new PassThrough(), fromApp = new PassThrough()
const lines = createInterface({ input: fromApp })[Symbol.asyncIterator]()
const next = async () => JSON.parse((await lines.next()).value)
app.serve(toApp, fromApp)
toApp.write(JSON.stringify({ t: 'hello', versions: [1] }) + '\n')
await next() // {t: 'hello', version: 1, sdk: 'node/…', methods: [...]}
toApp.write(JSON.stringify({ t: 'call', id: '1', method: 'hello.greet', params: null,
  ctx: { user: 'alice', admin: false, locale: 'en', request_id: '0'.repeat(32), deadline_ms: 60000 } }) + '\n')
await next() // {t: 'result', id: '1', ok: true, data: {message: 'Hello, alice'}}
```

## In a package

The App directory of the SPK is `/var/packages/<package>/target/app`. The
runtime starts `<entry>` there with
`/var/packages/<node.package>/target/usr/local/bin/node`, so the SPK depends
on that Package Center package (`install_dep_packages`). The SDK has no
dependencies: the package build copies `package.json` and `dist/` to
`app/node_modules/@synopackageland/app/`, or bundles the App and the SDK
into one file.

## Development

```sh
npm ci
npm run generate   # src/frames.ts and src/refusals.ts from spec/
npm test           # type-checks src and test, then node:test
npm run build      # dist/: ESM and .d.ts
```

`src/frames.ts` (json-schema-to-typescript, from
`spec/bridge/v1/frames.schema.json`) and `src/refusals.ts` (from
`spec/protocol/v1/errors.json`) are generated and committed; a test fails
when they differ from `spec/`. `scripts/gate.sh` also runs the tests, the
bridge's golden runner and the hello example on Node 22 in Docker
(`scripts/node22-test.sh`).

With bridge capability `webapi:true` and approved executor profiles, the existing
runtime service interface supports profile grants and calls:

```ts
const grant = await ctx.runtime('webapi.grant', {scope: 'calendar', lifetime: 'until-revoked'})
const result = await ctx.runtime('webapi.call', {delegation_id: (grant as {delegation_id:string}).delegation_id,
  api: 'SYNO.Cal.Event', method: 'list', version: 6, params: {}})
```

`webapi.grants` lists the caller's grants; `webapi.revoke` revokes one. Replies
contain the full DSM envelope. No mutation retries; reconcile unknown outcomes.
See spec/bridge/v1 and executor/README.md for the fixed permissions and limits.
