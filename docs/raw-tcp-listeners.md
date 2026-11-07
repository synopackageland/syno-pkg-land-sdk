# Declared raw TCP listeners

`ipc.ListenTCP(ctx, "mail_relay", port)` returns a `net.Listener` constrained by
the native Go package's build-bound manifest declaration. This supports private
protocols such as a loopback SMTP relay without permitting arbitrary `net.Listen`
constructors in shipped application binaries. Use a matching CLI with TCP policy
v1 support; look up matching released SDK and CLI versions before updating pins.
No release is made by this change.

```yaml
# dsm-package.yaml, schema 2
tcp_listeners:
  mail_relay:
    port: 2525
    # scope defaults to loopback: exactly IPv4 127.0.0.1
    port_range: [1024, 65535] # optional explicit configurable-port allowance
```

```go
import "github.com/synopackageland/syno-pkg-land-sdk/ipc"

listener, err := ipc.ListenTCP(ctx, "mail_relay", configuredPort)
if err != nil { return err }
defer listener.Close()
// Accept and serve the App's bounded, authenticated protocol.
```

A zero requested port selects the manifest default, rather than an ephemeral
port. Without `port_range`, only that fixed default is allowed. All ports must
be unprivileged integers, 1024–65535. Only the declaration selects the address:
`scope: loopback` (default) means `tcp4` on `127.0.0.1`; explicit `scope: all`
means `tcp4` on `0.0.0.0`. IPv6, DNS names, arbitrary host strings and privileged
ports are unsupported. Names use lowercase ASCII letters, digits and underscores,
start with a letter and have at most 64 characters. At most 64 declarations are
allowed. Duplicate default ports fail CLI validation; overlapping configurable
ranges are allowed and runtime binding decides conflicts. No DSM firewall rule,
service-port registration, installer UI, process supervision or root authority is
created. Use the existing jobs worker or daemon to host a long-lived service.

The CLI normalizes the policy and binds it, plus the package identity, into both
CGI and worker binaries with linker flags. The SDK does not read an environment
variable, a writable policy file or policy supplied by the caller. A binary with
no compiled policy/identity, an undeclared ID or an out-of-policy port fails
closed. Local SDK source replacement is a development mechanism, not a published
SDK pin. `check` validates the pinned/resolved SDK supports policy v1.

Use a **direct call with a literal ID** at each call site. Constructor aliases,
dynamic IDs and undeclared IDs fail the shipped-source audit. Declaring TCP does
not exempt any `net`, `tls`, syscall or HTTP constructor, file, directory or
handler from the existing audit. HTTP routes still use the SDK HTTP registry.
The auditor and compiler policy provide a supported source boundary, not an OS
sandbox against malicious code, reflection, plugins, cgo or custom syscalls.

Loopback binding uses the same internal bind primitive as `ipc.ReserveLoopback`;
the raw listener retains the bound socket without a release/rebind handoff.
Reservations still expose no listener and cannot bypass declarations. Kernel bind
failure is authoritative; a prior probe never guarantees ownership. `Close` is
idempotent. Context cancellation closes the listening socket and unblocks
`Accept`, but **does not close already accepted connections**. The App owns those
connections, deadlines, shutdown, concurrency limits, TLS/STARTTLS, authentication,
frame/message limits and upstream authorization. Other NAS processes can connect
to loopback; it is not a DSM identity boundary.

`inspect --json` reports the archived normalized `tcpListeners` policy. That is
build metadata, not proof of runtime binding, firewall reachability, SMTP delivery
or a package signature. No NAS acceptance is established by local tests.
