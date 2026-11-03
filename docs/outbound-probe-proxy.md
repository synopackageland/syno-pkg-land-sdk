# Bounded subprocess probes

`outbound.StartProbeProxy(ctx, outbound.ProbeProxyOptions{})` creates one
SDK-owned, authenticated proxy for a package-account HTTP(S) probe. It does not
accept an App HTTP handler, listener, resolver or dialer. The App passes `URL()`
to the subprocess's explicit proxy option and uses `Context()` for its runner.
Always close the handle when the probe ends. Run as the package account; root
is refused.

```go
proxy, err := outbound.StartProbeProxy(ctx, outbound.ProbeProxyOptions{})
if err != nil {
    return err
}
defer proxy.Close()
// Run the fixed probe argv with its explicit proxy options set to proxy.URL().
// Stop the subprocess group when proxy.Context() is canceled.
// Never log or persist proxy.URL(): it contains an ephemeral credential.
```

Every HTTP request and HTTPS CONNECT authority passes the existing public URL
policy. Every resolved address must be public, including mixed DNS answers;
connections dial only those checked IPs without another DNS lookup. HTTPS is
not decrypted and the child still authenticates the original server certificate.
The proxy never follows redirects itself: each subsequent request or tunnel
must pass the same checks. `AllowURL` may impose an additional restriction;
it cannot relax the SDK's public-address policy. HTTPS exposes its CONNECT
authority, not its encrypted path, to that restriction.

Each probe has hard ceilings of 64 accepted client connections (including idle
or unauthenticated connections), 64 proxy requests/tunnels, 64 outbound connection
attempts and 32 MiB total upstream wire traffic in both directions. Options may
lower those ceilings, with zero selecting the defaults; negative or larger
values are refused. The SDK does not add a total wall-clock deadline. The App
must bound tool output, disable unneeded retries, honor cancellation and handle
any existing child inactivity limits. HTTP headers are bounded at 64 KiB.

`Denied()` records a policy refusal even when the tool later returns other
output. `Exhausted()` records a consumed work budget. Both are distinct from
ordinary tool errors and parent cancellation; the App decides which results
may fall back to another downloader. Do not fall back on a policy refusal.

`Close()` is idempotent and cancels the proxy context, closes admission,
accepted and hijacked connections and in-flight upstream sockets. Parent
cancellation does the same. Proxy authentication is required for each request
and its credentials are removed before HTTP forwarding.

This is an outbound proxy boundary for a cooperating subprocess's configured
HTTP networking. It is not an OS sandbox: it cannot prevent a subprocess,
external runtime or alternate transport from opening a socket outside the
configured proxy, and it does not govern a later download automatically.
