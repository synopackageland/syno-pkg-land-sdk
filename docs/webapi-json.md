# JSON WebAPI reply limits

`webapi.call` accepts a DSM JSON envelope of up to **20 MiB (20,971,520
bytes)** after removing insignificant JSON whitespace. The limit includes
`success`, `data`, and any DSM error fields. The complete envelope is returned,
including `success:false`. Large numbers, string escapes, Unicode, and member
names retain their original JSON representation; the executor never converts
the payload to floating point or rebuilds a decoded object tree.

The shared definition is [limits.json](../spec/protocol/v1/limits.json).
`node scripts/protocol-limits.mjs` generates the executor, Go client,
TypeScript, Node, and Python bounds; `--check` detects drift. Socket and App
bridge replies allow another 64 KiB for their envelopes, for a total of
21,037,056 bytes including the newline. Ordinary executor requests still have
a 64 KiB limit, WebAPI params still have a 32 KiB limit, and `root.run` keeps
its 64 KiB inline reply and existing staging contract. Raw WebAPI bodies keep
their separate approved limit and 32 KiB inline threshold.

The executor consumes stdout through its streaming command interface,
bypassing the generic command's 1 MiB output capture. Pretty-print whitespace
does not consume the reply quota. An unbounded amount of whitespace does not
increase retained memory; the existing operation deadline and cancellation
still apply. No new wall-clock deadline is introduced.

## Oversized replies

When the next compact byte would exceed the limit, the executor stops the
command group and returns a nonretryable `WEBAPI_REPLY_TOO_LARGE` refusal
(HTTP mapping 413). Its public message includes both sizes in bytes:

```text
WebAPI compact reply size is at least 20971521 bytes; limit is 20971520 bytes
```

The size is a **lower bound**, not a measurement of the whole DSM response:
capture stops at the first excess byte. The executor neither truncates a
success nor continues buffering or spooling to measure the remainder.
Malformed envelopes, execution failures, and uncertain audit completion still
return `WEBAPI_UNCERTAIN`. An oversized mutation may already have completed;
neither refusal authorizes automatic mutation retries.

Go callers receive an `*api.Error` with that code and message. Node callers
receive `ServiceError`; Python callers receive `synopkgland_app.Error`.
The TypeScript protocol decoder exposes it in the `RefusalCode` union.
Apps should catch the code and display an unknown/unavailable state, or offer
an approved paged read if that API supports one:

```go
var refusal *api.Error
if errors.As(err, &refusal) && refusal.Code == delegate.CodeWebapiReplyTooLarge {
    // Keep the UI usable; the requested state is unknown.
}
```

## Executor memory

No JSON reply is written to `/tmp` or to the raw/root staging directory.
The executor retains one compact payload. Capture grows through 32 KiB,
256 KiB and 1 MiB buckets before allocating the 20 MiB ceiling; ordinary
storage/process reads therefore need not allocate a full 20 MiB. The old
buffers together account for at most 1.282 MiB of transient allocations.
It never retains the pretty output, a decoded data tree, or
an additional serialized socket frame. Large scalar values are validated
without decoding or copying them.

Duplicate member checks keep eight-byte offsets into that same payload.
An initial scan counts object members and allocates their index exactly
once; nested objects reuse the index. Each member needs at least five compact
bytes, so even a pathological envelope consisting of many small members uses
at most another 32 MiB for this index. Validation also uses a parser stack,
bounded by Go JSON's syntax nesting limit. A normal maximum-size envelope with
one large string needs only a few index entries.

JSON capture shares the two output slots with root programs and raw WebAPI
transfers. A slot stays held until the socket write finishes, so slow readers
cannot queue up additional resident 20 MiB payloads. Saturation returns
`ROOT_BUSY`. The conservative bound for two JSON captures' payloads, key
indexes, and the small old capture buffers together is **106.6 MiB**, plus
parser/goroutine stacks, command pipe buffers, allocator overhead, and the
executor's existing state. This is a bound on live JSON working storage,
not an RSS ceiling: Go may retain freed heap pages.

The package's Go runtime, Node/Python App, and browser have separate heaps.
Those clients decode JSON into their language's normal values and may retain
both encoded frames and decoded values. Large object trees can use
substantially more memory than their wire size. Apps should process and release
large results promptly and prefer paging when available.

## Delivery

The executor binary changes and requires the normal administrator approval of
its new digest. Rebuild the App with the updated SDK, executor manifest and
matching immutable executor module release. Existing installed Apps retain
their bundled executor and old limits until rebuilt and upgraded.

The CLI reads `executor-manifest.json`, resolves the manifest's executor
version, builds that module and verifies its architecture digests. No new CLI
size check or protocol change is required for manifest-aware CLI versions.
Publish the matching executor module tag as part of the SDK release; reusing
the preceding executor release would retain the original 48 KiB cap.
