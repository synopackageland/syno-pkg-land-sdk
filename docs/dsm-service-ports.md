# DSM service and port definitions

Apps can read DSM's installed service declarations as their package account,
without root, an executor or a background service. A declaration does not prove
a listening socket, reachability, permission or an available port. Use it for
port suggestions, naming and potential conflict checks, then verify what the App
actually needs. These APIs describe the current SDK.

## Go

```go
import "github.com/synopackageland/syno-pkg-land-sdk/dsmservices"

definitions := dsmservices.Read()
for _, definition := range definitions {
    for _, destination := range definition.Destinations {
        // Inclusive [First, Last], with TCP and/or UDP. No range expansion.
        _ = destination
    }
}
```

`Read()` uses `Dirs` in order:

1. `/usr/syno/etc/services.d` — DSM's live definitions, including changed DSM ports
2. `/usr/syno/etc.defaults/services.d` — fallback filenames absent from the live directory
3. `/usr/local/etc/services.d` — packages and Container Manager `docker_*.sc` files

Pass directories to `Read(dirs...)` for fixtures or an alternate root. The first
successfully read filename hides the same filename in later directories. Reads
are on demand, uncached and best effort: missing/unreadable directories or files
are skipped. Only regular `*.sc` entries directly in each directory are read;
subdirectories such as `tmp/` and symlinks are skipped. The reader skips directories
with over 512 entries and files over 64 KiB. It starts no timers or watchers.

`Parse(filename, bytes)` tolerates bare or quoted INI values and skips odd lines,
invalid port tokens and incomplete lines beyond the scanner's 64 KiB limit.
`Definition` retains section `ID`, source `File`, `Title`, `Desc`, and DSM
`TitleKey`/`DescKey` translation keys. `Destinations` retains inclusive ranges
using `-` or `:`, with TCP/UDP flags for `tcp`, `udp`, `all` and mixed protocols.
`Ports` is the sorted, deduplicated single-TCP-port projection (at most 1024),
which excludes declared ranges and UDP-only ports. Neither projection classifies
ports as web services; that decision belongs to the App.

Apps that automatically follow declarations can use `ParseStrict(filename, bytes)`
and reject its error. It preserves each `dst.ports` line in file order, with the
section and valid title preceding it, and fails on malformed port syntax,
half-quoted relevant values, missing sections and scanner overflow. A title is
accepted only when trimmed printable UTF-8 of 1–80 runes; an invalid title leaves
the preceding title unchanged. Returned definitions carry `Destinations`; the
caller owns range expansion limits, deduplication, caching and withdrawal policy.
`ParsePortSpec(value)` provides the same strict destination syntax independently.

## Frontend

```ts
import {readDsmPorts} from '@synopackageland/sdk'

// Render first; this optional enrichment must not block App startup.
void readDsmPorts(ctx).then(ports => {
  // id, name, desc, protocol, ports (singles), ranges (inclusive bounds)
})
```

The helper calls `SYNO.Core.Service.PortInfo`, `load`, version 1, using
`ctx.webApi` when provided by the DSM window. For older windows it POSTs to the
same-origin `/webapi/entry.cgi` using the current `ctx.files.token()` as
`X-SYNO-TOKEN` and DSM's form token. It never sends credentials cross-origin.
There is no backend credential relay or privilege escalation.

PortInfo requires an administrator's DSM session. `name` and `desc` already use
that session's language; `id` matches the Go definition's section ID. Both UDP
and TCP declarations remain available, as do `all` and mixed protocol values.
Single ports and ranges are validated within 1–65535; ranges are not expanded.
Missing sessions, administrator refusal, transport/HTTP/JSON errors, malformed
lists and a five-second deadline resolve to `[]`. There is no automatic retry,
cache or polling. An empty result says nothing about whether services exist;
keep the backend's own names as a fallback. A timed-out window WebAPI call may
still finish in the host, but its late result does not change the returned list.

The fixture tests preserve the Connect reader and Follow services contracts.
No live NAS acceptance is claimed by these tests.
