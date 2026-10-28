# App CGI requests

Since 0.9.9. The key words MUST, MUST NOT, SHOULD and MAY are to be
interpreted as described in RFC 2119.

The Go `api.Server` answers the App's browser page behind the package CGI,
for a Go App and, through the bridge runtime, for a Python, Node or Rust App.
Every App MUST answer each case in `../testdata/golden/api.jsonl` byte for
byte; `golden_test.go` writes the file and `../bridge/golden_test.go` replays
it through each language.

## Origin guard

App entry admission precedes route permissions, including public App routes,
batches and transfers. Only an App without an entry floor can dispatch a public
business route with an anonymous caller. The SDK's fixed identity ceremony
remains reachable for sign-in. See [App entry enforcement](../docs/app-entry-access.md)
for the packaged policy, audit and local IPC boundaries.

DSM's SynoToken does not protect the CGI against cross-site requests. It is
one value for the whole session, and with DSM's "Improve protection against
cross-site request forgery attacks" off every session shares one placeholder
token that DSM does not check. The JSON content type forces a CORS preflight,
which DSM never answers, but a form post or an upload needs none. The server
therefore asks the browser where each request came from, before it reads the
body or authenticates:

1. When `Sec-Fetch-Site` is present, only `same-origin` passes; anything else
   (`same-site`, `cross-site`, `none`) is `cross_origin`. A page cannot set
   this header, so it also decides behind a reverse proxy that rewrites
   `Host`. Another port of the NAS, such as another App's Application Portal
   entry, is `same-site` and refused: cookies do not separate ports.
2. Otherwise `Origin`, else `Referer`, MUST name the request's host: the
   `Host` the CGI received, or the first `X-Forwarded-Host` value a reverse
   proxy set. A page cannot send either header cross-site without the
   preflight. The scheme is not compared, since a proxy that terminates TLS
   hides it. An opaque `Origin: null` or an unparsable value is
   `cross_origin`.
3. A request with none of the three headers is `origin_required` unless its
   method is GET or HEAD. Every current browser sends `Origin` on a POST; a
   script that calls the CGI MUST send it too. A GET download without them
   passes, as a download does not change state.

An Application Portal entry on its own port or host serves the App's page
and its CGI from that same origin, so rule 1 or 2 holds there as on DSM's own
port.

The standalone identity transport (`identity.Service.Wrap`) applies the same guard first, to its own
`identity.*` methods, to business requests and to transfers, and answers the
same codes.

## Deadline

A request has one deadline, 60 seconds unless the App sets another
(`api.WithRequestDeadline(handler, api.Timeout(d))`). The server keeps a
deadline it finds and applies 60 seconds only to a request without one. A
handler still running at the deadline answers `request_timeout`; a request the
browser canceled answers `request_canceled`. The TS client waits two seconds
longer than the deadline (`clientTimeout(ctx.requestTimeout)`), so the
server's reply arrives first.

## Error codes

Refusals travel as `{"success":false,"error":{"code","message"}}` with HTTP
200. Clients MUST match on `code`.

| Code | Meaning |
|---|---|
| `cross_origin` | Another site, port or opaque origin sent the request |
| `origin_required` | A request that changes state proved no origin |
| `request_timeout` | The handler passed the request deadline |
| `request_canceled` | The browser went away before the reply |
