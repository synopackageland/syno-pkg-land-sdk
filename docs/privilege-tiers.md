# Privilege tiers

The App CGI and package worker run as the package account. DSM administrator
admission is an application permission; it does not change their Linux UID.
Generated builds bind `ipc.executorTier=none`, including Apps that use SDK
rootOps or delegate capabilities. They cannot serve a root peer protocol.

SDK executor capabilities use the existing separately protected executor
payload, approved digests and fixed operations. They never run the App CGI
or package-writable worker as root.

An App may also distribute an optional, separately installed executor helper.
Its protected installer and explicit administrator approval must pin executable
bytes, fixed operations and lifecycle behavior. It requires its own review;
the IPC transport provides no installer or root-launch capability. Keep the
executable, configuration and privileged dependencies root-owned and
non-writable by the package account. Disable admission before revoke/stop and
terminate privileged work using the helper's verified lifecycle.

The protected helper alone may bind `ipc.packageID=<package>` and
`ipc.executorTier=executor` via `-X` linker flags. `ipc.ServePeerUnix` then
requires effective UID zero at runtime, a protected package-scoped socket path,
and a numeric peer policy. A flag is not administrator approval. Never bind an
ordinary CGI/worker to this tier or launch it as root. `ipc.DialPeerUnix` only
authenticates a running root helper; it neither approves nor starts it.

The [HTTP and IPC contracts](http-boundaries.md) describe the peer transport,
connection limits and callback responsibilities. For custom build hooks, run
CLI `audit-entry` for each shipped helper, daemon and CGI entry on every target
architecture. A clean import-graph audit checks entry construction, not the
safety of privileged operations, protected installation or device lifecycle.
