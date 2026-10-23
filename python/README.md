# Python App SDK

Package `synopkgland_app`: write a package's backend methods in Python. The Go
runtime (the package's CGI) checks the request's origin, the DSM session, the
entry gate, the method's policy (`permissions.json`) and the deadline, then
starts the App once per call and hands it the verified call over stdin and
stdout (channel `spec/bridge/v1`, mode `exec`). The App never sees the DSM
session and never runs as root.

- Standard library only; Python 3.8 or later, so DSM 7's `/usr/bin/python3`
  (3.8) runs it with nothing installed.
- One process per call: keep nothing in globals between calls, and expect a
  start-up cost of about 50 ms (x86) to 150 ms (ARM) per request.

## Quick start

```python
# app/main.py
from typing import Any, Dict

from synopkgland_app import App, Context, Error

app = App()


@app.method("notes.greet")
def greet(ctx: Context, params: Any) -> Dict[str, str]:
    if not ctx.user:
        raise Error("anonymous", "sign in first")
    return {"message": "Hello, %s" % ctx.user}


if __name__ == "__main__":
    app.run()
```

The package build lists the methods in `bridge.json` and their policy in
`permissions.json`; the runtime registers routes from those files, never from
the App. A method the App registers must be listed there, and the other way
round, or every call fails.

```
$ python3 app/main.py --describe
{"sdk": "python/0.17.3", "versions": [1], "methods": ["notes.greet"]}
```

`examples/hello/app.py` is a complete App; `bridge/runtime_test.go` runs it end
to end through the runtime, as a package would.

## API

### `App`

- `@app.method(name)` registers the handler of method `name`
  (`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`, for example `notes.save`). A name
  that does not match, or one registered twice, raises `ValueError`.
- A handler is `handler(ctx, params)`. `params` is the request's `params` as
  `json.loads` gives it (`None` when absent). It returns any JSON value
  (`None` is `null`); the browser gets `{"success":true,"data":…}`.
- `app.run()` serves the one call the runtime sends, then returns. It exits
  with status 1 when run as root, and with status 2 unless
  `SYNOPKGLAND_BRIDGE=stdio` (the runtime sets it). `print()` and child
  processes write to stderr, which DSM logs; stdout carries only frames.
- `python3 <entry> --describe` prints one JSON line: `sdk`, the channel
  `versions` it speaks and the sorted `methods`. It does not serve, so a
  build machine may run it as any user.
- `app.serve(rfile, wfile)` answers one call on any pair of binary streams;
  for tests.

### `Context`

| Member | |
|---|---|
| `user: str` | the caller's DSM account; `""` for a public method |
| `admin: bool` | whether the caller is an administrator |
| `locale: str` | the browser's first `Accept-Language` tag, or `""` |
| `request_id: str` | 32 hex digits, unique per request |
| `deadline: float` | the `time.monotonic()` value at which the request ends |
| `deadline_ms: int` | the time that was left when the runtime sent the call |
| `cancelled: threading.Event` | set when the request is over: deadline, browser gone, or channel closed |
| `runtime(op, params=None)` | asks the runtime for a service; returns its `result` |

The runtime decides the deadline; an App cannot extend it. Python cannot
interrupt a running function, so a long handler should check
`ctx.cancelled` (or `ctx.deadline - time.monotonic()`). It does not have to:
one second after the request ends, the runtime kills the App's process group.

`ctx.runtime(op, params)` raises `Error` with the runtime's code when the
runtime refuses: `unknown_service` for an op it does not offer, or a protocol
v1 code such as `SCOPE_DENIED`. Once `ctx.cancelled` is set, it raises
`Error("request_canceled", …)` without asking. It may be called from several
threads.

### `Error`

`raise Error(code, message)` sends `{"success":false,"error":{"code":…,"message":…}}`.
`Error.bad_request(message)` is `Error("bad_request", message)`.

- `code` is the App's own `^[a-z][a-z0-9_]{0,63}$` code, or an upper-case
  refusal of `spec/protocol/v1/errors.json` (for example a `SCOPE_DENIED` it
  got from a service), which `@synopackageland/sdk` translates in the browser.
- The runtime's own codes (`session_required`, `admin_required`,
  `app_privilege_denied`, `auth_unavailable`, `unknown_method`,
  `cross_origin`, `origin_required`, `request_timeout`, `request_canceled`,
  `request_too_large`, `json_required`, `method_not_allowed`,
  `app_unavailable`) and any other code reach the browser as `internal`; the
  SDK writes why to stderr.
- `message` is cut at 1024 bytes.
- Any other exception sends `internal` with the message `internal error`; the
  traceback goes to stderr only.

## Types

`synopkgland_app/frames.pyi` has a `TypedDict` for every frame of
`spec/bridge/v1/frames.schema.json`. It is a stub: type checkers read it, and
the package ships `py.typed`. Import it only for type checking:

```python
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from synopkgland_app.frames import Context as ContextFrame
```

`scripts/python-frames.sh` regenerates it with datamodel-code-generator (pinned,
run with uv, at build time only); `--check` fails when it is stale.

## Packaging

The package build copies the directory `synopkgland_app/` into the SPK as
`app/vendor/synopkgland_app/` (no pip on DSM) and sets the App's
`PYTHONPATH` to that `vendor` directory. At run time the App needs
`__init__.py` and `_refusals.py`; `frames.pyi` and `py.typed` are for type
checkers.

## Limits

- Numbers arrive as values, not literals: `2.50` becomes `2.5`. Send 64-bit
  IDs and sizes as strings. Integers keep their precision.
- One call per process; no resident mode in v1.
- No streaming transfer and no runtime services yet: in v1 every `ctx.runtime`
  call is answered `unknown_service`. Later versions add files, delegate and
  background jobs.
- `data` must stay within the runtime's JSON limits (depth 32, 65 536
  structural bytes) or the browser gets `internal`.

## Development

```
cd python && python3 -B -m unittest discover -s tests   # this Python
scripts/python38-test.sh                                # Python 3.8 in Docker
go test ./bridge/ -run 'Golden|Hello|PythonSDK'         # through the Go runtime
```

With bridge capability `webapi: true` and approved executor profiles, use the
same runtime interface as files services:

```python
grant = ctx.runtime("webapi.grant", {"scope": "calendar", "lifetime": "until-revoked"})
result = ctx.runtime("webapi.call", {"delegation_id": grant["delegation_id"],
    "api": "SYNO.Cal.Event", "method": "list", "version": 6, "params": {}})
```

`webapi.grants` lists the caller's grants; `webapi.revoke` revokes one. The result
is the full DSM envelope. Never retry an uncertain mutation automatically.
The executor enforces profiles, current permissions, runner, bounds and audit.

## DSM file sessions

The default Go runtime no longer forwards a browser DSM session to File
Station over HTTP. Its legacy `files.list`, `files.info` and `files.read`
services return `unsupported`; wrappers never receive DSM credentials.
Use the frontend SDK's same-origin file client for interactive operations.
Backend writes or grants require independent caller-bound authorization;
browser-only Apps do not acquire an executor. See the repository's
`docs/dsm-session-transport.md` for the App migration checklist.
