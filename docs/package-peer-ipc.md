# Package-account peer IPC

`ipc.StartPackagePeerUnix` serves a bounded Unix peer protocol as the package
account on Linux. It requires no root helper, executor build or approval and
refuses to run as root. Use a direct constructor call so the CLI entry audit
can recognize the SDK boundary (CLI 0.46.0 or later).

```go
server, err := ipc.StartPackagePeerUnix(ctx, socketPath,
    func(ctx context.Context, peer ipc.PeerCredentials) bool {
        return allowedPackageUID(ctx, peer.UID)
    },
    func(ctx context.Context, conn net.Conn, peer ipc.PeerCredentials) {
        defer conn.Close()
        // Decode a bounded frame; validate its operation against current
        // authorization using peer.UID, never an identity from the body.
        handleRequest(ctx, conn, peer.UID)
    })
if err != nil {
    return err
}
defer server.Close()
return server.Wait()
```

The SDK reads UID, GID and PID from Linux `SO_PEERCRED` for every accepted
connection. A missing callback or a false result closes the peer without
calling the handler. The callback must return promptly and honor cancellation;
the handler must still validate package identity, bounded frames and current
operation authority. An admitted Unix UID does not grant a DSM session, App
privilege or root permission. The SDK does not infer policy from account names.

Pass a clean absolute named `.sock` path, for example
`/var/packages/ExampleApp/var/peers/control.sock`. Its parent must already exist
and be trusted. DSM's package `var` anchor may be a platform-owned symlink.
The SDK pins that parent and refuses a symlink for the dedicated peer directory
or socket. It creates or verifies a package-owned directory with exact mode
0711, and binds a package-owned socket with exact mode 0666. Those modes let
other package accounts connect without listing the directory; **the credential
callback and request handler provide authorization**, not filesystem secrecy.
The SDK never repairs existing owner/mode mismatches or changes process umask.

A nonblocking directory lock is held for the listener's lifetime: use one
directory per listener. Existing regular files, symlinks, wrong owners or modes
and live sockets are refused. Only an owned mode-0666 socket whose connection
attempt returns `ECONNREFUSED` is removed as stale, after checking that its inode
has not changed. Close removes only the inode this server created, preserving
any replacement. The directory remains for reuse.

The `PeerServer` handle has the same lifecycle as the executor peer API:

- Eight slots bound authorization callbacks, handlers and retained peers together.
  SDK 0.48.0 adds the explicit capacity option; the original API is unchanged.
  Packages retaining many streams may explicitly use
  `StartPackagePeerUnixWithOptions(..., PackagePeerOptions{MaxConnections: 316})`.
  Zero retains the default eight; values outside 1–1024 are refused. The chosen
  limit still includes pending authorization and there is no overflow queue.
  This does not increase a downstream protocol's own session/handshake limits or
  change peer identity checks. Size the bound for retained peers plus probes;
  the handler remains responsible for its own authenticated-session admission.
- Connections have a five-second initial deadline; set an explicit deadline
  for longer exchanges. Authorization callbacks themselves must honor context.
- The handler owns its connection, including after returning. Close it when done.
- `StopAccepting` closes admission and cleans the socket while preserving peers.
- `Wait` waits for admission and cleanup, not callbacks or retained peers.
- `Close` or parent cancellation cancels callbacks and closes every accepted peer,
  including peers retained after `StopAccepting`.

Non-Linux builds compile but return an unsupported-platform error. There is no
unauthenticated fallback. This API does not replace `StartPeerUnix` or
`ServePeerUnix`: those retain their root-owned 0660 socket, exact single-UID
policy, protected executor path and separately approved executor-tier binding.
Private same-package HTTP remains `ipc.StartUnix` / `ipc.ServeUnix`.
