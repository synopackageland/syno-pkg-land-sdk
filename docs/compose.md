# Upstream Compose packages

## Nonroot runtime configuration

Services with a root-only image entrypoint can explicitly replace it with an
upstream executable and opt into `storage_init.runtime`. This is not transparent
emulation of arbitrary root setup. The official image and its `/etc` remain
unchanged; no root, chown, Docker socket or implicit shell is used.

```yaml
compose:
  services:
    web:
      network: host
      listener: {env: LISTEN, format: host-port}
      storage_init:
        directories: [{base: /tmp, path: app-runtime}]
        runtime:
          environment:
            CLIENT_SECRET: {oidc_client: reader}
            SESSION_KEY: {secret: browser-session}
          files:
            - target: /tmp/app-runtime/server.conf
              source: /etc/server/reference.conf
              replace: [{old: 'Listen 80', new: 'Listen ${LISTEN}'}]
          prepare:
            - argv: [/usr/bin/app, initialize]
              unless_exists: /tmp/app-runtime/initialized
              success_codes: [0, 3]
          periodic:
            - argv: [/usr/bin/app, refresh]
              interval_seconds: 1800
              timeout_seconds: 300
```

`listener` is mutually exclusive with legacy `address_env`. Format `host`
injects `127.0.0.1`; `host-port` injects `127.0.0.1:<compose.web.port>` with no
arbitrary address or remap. It requires the existing explicit nonroot host/OIDC
topology and internet egress. Policy/upstream environment cannot override it.
The legacy bare-host contract is unchanged. IPv6/wildcard listener selection is
intentionally unsupported.

Runtime files are direct children of declared private directories on writable
mounts. Declare either literal `content` or a regular bounded image/private
`source`; exact replacements fail when absent unless `optional: true` is set.
There is no env/shell/template evaluation: native `${VAR}` stays literal for
the upstream config parser. `after_prepare: true` configures installed data
after initialization. `if_absent: true` retains an existing private file.
Files are atomically replaced at mode 0600 through pinned directory descriptors;
symlinks, public/foreign directories, hardlinked targets and path escapes fail.
Image assets are never written. Rootfs read-only and writable tmpfs/volume
declarations remain explicit Compose choices.

Prepare uses absolute fixed argv with no inferred shell and defaults to exit 0;
only explicitly declared `success_codes` are accepted. `unless_exists` names a
private direct child, never a host path or shell predicate. Every command shares
the one startup budget, gets a private umask, and failure prevents main startup.
Its output/argv/env are omitted from SDK errors because upstream initialization
may print credentials. The main application inherits stdout/stderr as before.
The SDK exports actual `SYNOPKGLAND_UID`/`SYNOPKGLAND_GID` for native config syntax.

At most 32 files, 16 prepare steps, 32 env bindings and 8 interval tasks are
accepted; the serialized initialization is limited to 48 KiB, image files to
256 KiB. Periodic tasks run without overlap, after each declared interval
(1–86400 seconds), with an explicit 1–900 second timeout. A failed task emits
only its index and can retry next interval. The main exits with its native code;
container stop terminates its group and cancels/reaps tasks. This small scheduler
does not add cron syntax, account actions or arbitrary service exec.

Runtime env bindings reference generated secrets, never literal credentials.
`oidc_client` must be registered in this package's OIDC gateway. The package-UID
preinst materializer creates private seeds in `compose/secrets` and mounts only
the referenced files read-only into the container. The gateway adopts the same
client seed into retained provider state; upgrades reuse it and mismatches fail
closed. This also works before DSM creates pkgvar: prepare never creates that
DSM-owned directory. A generic `secret` key is generation-local and rotates on
replacement, intentionally ending that application's encrypted browser sessions.
Secret values appear only in private installed files and the running container
environment, not the SPK, plan, lock, Compose env, argv or lifecycle output.
Existing non-Compose and legacy storage-init clients keep their old behavior.

This capability does not claim that an arbitrary image can run nonroot. Consumers
must verify its module/runtime paths, supported upstream configuration, exact
argv, readiness, stop/start and data preservation on the target DSM.

The CLI converts schema-2 declarations to an SPK. The SDK owns Compose planning,
identity gateway and lifecycle control. There is no separate wrapper container
or SSO Server. Upstream files stay byte-for-byte unchanged, including comments.

```yaml
schema: 2
package: MediaApp
version: 0.1.0
build: 1
displayname: Media App
description: Upstream application
maintainer: Example
os_min_ver: "7.2.1-69057"
arches: [x86_64, aarch64]
capabilities: {}
compose:
  files: [compose.yaml, site.yaml]
  env_files: [.env]
  profiles: []
  web: {service: web, port: 8080, health: {path: /health, status: 200}}
  network: {egress: isolated}
  # Private package binds are rejected by DSM build; keep compatible upstream named volumes.
identity_gateway:
  mode: none
  access: dsm-users
  listen: {http: 28000}
ui: {launch: new-page}
```

Compose uses the SDK pinned by the CLI templates unless `compose.sdk` or
`identity_gateway.sdk` explicitly selects an SDK. Use aligned releases that
implement the App data contract.

No App backend, dummy CGI, UI directory or App-authored helper payload is required. Omit `web`
and `identity_gateway` for headless projects. Optional `compose.helpers` names
architecture-specific native payloads for token/account adapters. All lifecycle
scripts run as the package account; containers are managed by Container Manager.
Named volumes are kept, including when delete-data is selected;
see [App data and uninstall](compose-app-data.md) for inventory and honest reporting.

## Planning and artifacts

The pinned compose-go reference loader merges ordered files, env files and
profiles. Process environment never supplies interpolation values. Local include,
extends, env_file and label_file dependencies are bounded, confined and copied
without symlinks. Remote/interpolated dependencies are rejected. CLI import uses
the SDK inventory, not a second parser for dependency semantics.

`compose/upstream/` preserves input bytes. `generated.override.yaml` is a real
reference-loader replacement override: lists such as ports and mounts use
`!override` so original wildcard publications cannot survive list merging.
`docker-compose.yml` is the final tag-free worker document. `compose.lock.json`
records input/effective hashes, image/platform inventory, fixed identities,
mounts, networking and explicit acceptance blockers. `images.json` contains the
registry manifests/config bytes needed for offline image-volume verification.
Inspection replays the trusted SDK planner over the packaged inputs and compares
actual effective, override, lock, launcher, gateway, resources and lifecycle.

Build resolves anonymous registry metadata and pins the Linux platform manifest.
It verifies pinned upstream index/manifest digests and image config hashes, and
turns image-declared anonymous VOLUMEs into stable package-namespaced volumes.
Private registry credentials are not inherited. `SYNOPKGLAND_COMPOSE_REGISTRY_MIRRORS`
is an explicit build-host JSON map of registry names to HTTPS origins; it changes
metadata transport only, never the repository or runtime reference. Mirrors are
a build operator trust decision. `check`/`inspect` remain offline.

## Support boundary

| Feature | Current behavior |
| --- | --- |
| Files in one directory, profiles, anchors, depends_on, healthcheck | Reference-loader semantics retained; healthcheck timing receives the slow-NAS floor below; DSM worker compatibility needs device acceptance |
| Named local volumes | App data by default; kept on all uninstall choices; exact undeleted names reported when delete-data is selected |
| Private bind data | DSM build rejects COMPOSE_PRIVATE_BIND_TIMING_UNSUPPORTED: clean-install preinst precedes pkgvar creation, while worker acquire precedes postinst; no root initialization or automatic volume conversion |
| Image VOLUME | Build inventories verified image config and assigns a stable named volume |
| Existing external volume | Rejected without trusted admin ownership/driver readback |
| Existing Shared Folder | Native install selection plus DSM package-user ACL; dynamic admin-after-install authority stays blocked |
| Bridge | Default no-publish dedicated internal bridge with a package-derived fixed IP; availability requires App health, TCP and the current lifecycle acquire record; no Docker identity attestation |
| Host | Explicit verified OIDC web consumer with address_env forced to 127.0.0.1; no container-root; internet access is visible |
| Egress | isolated internal networks, or explicit internet outbound bridge; fine allowlist declarations fail |
| Public ports | All upstream publishes removed; the host gateway dials the declared web service on its fixed bridge IP; auxiliary ingress is unsupported |
| Root | Fixed package UID/GID by default; per-service container-root requires reason, bridge and no host binds; only CHOWN/FOWNER/SETUID/SETGID opt-ins |
| Build, hooks, privileged, host PID/IPC, arbitrary devices, sockets | Rejected; Intel render devices require the explicit GPU capability below |
| Secret/config file resources, plugins, replicas/deploy | Rejected until their bounded delivery/runtime contract exists |
| Subpath hosting | Unsupported; use the app's dedicated origin at / |

Container-root is distinct from host elevation. Named-volume ownership and an
image's arbitrary-UID compatibility are not proved by a valid plan. Test the
actual immutable image, initialization, health, UID/capabilities and restart.
Unknown compatibility is never a certification result.

Use the upstream official registry image, locked to its verified platform
digest. Container Manager downloads that image during installation. The SPK
carries SDK code and verified registry metadata. For a declared, shell-free
nonroot initializer supplied by the
SPK and a runtime write probe, see [Nonroot container storage](container-storage.md).

## Shared Folder authority

Containers use a fixed package identity. **They do not inherit the signed-in
user's File Station ACL.** App library/share settings control which app users
see mounted data. Read-only prevents source changes; read/write can permit file
modification and deletion. DSM identity/account bridge and file authority are
separate features. Per-DSM-user filesystem access requires a File Station session
or Broker Adapter; it is outside this change and cannot be obtained through a mount.

For shared data, use a dedicated folder with an optional existing-folder selection:

```yaml
capabilities: {composeMounts: true}
install:
  sharedFolder: {mode: dedicated, key: media, name: ExampleApp, permission: ro}
compose:
  shares:
    media:
      select: install
      # Omitted permission defaults to ro. For rw, set rw in both declarations.
      mounts: [{service: web, target: /media}]
```

The share key and permission must match `install.sharedFolder` exactly. This
route supports `mode: dedicated` and `mode: select`. DSM's data-share resource
creates the dedicated folder and grants the fixed package account the declared
permission. No root helper or Docker socket is given to the App. The optional
existing-folder field blocks Next unless its type and package-account access can
be verified. Host paths are never wizard answers.

Package-account preparation resolves an existing folder on `/volumeN`; before a
new dedicated folder is created it uses DSM's selected package volume. Missing,
ambiguous and symlinked existing sources fail closed. Every mounted service uses
the fixed package UID:GID, drops Linux capabilities and has no-new-privileges.
Container-root cannot mount Shares. Start repeats the kernel permission check;
loss of access reports the folder and RO/RW recovery in the localized hook log.

Dedicated-mode dynamic upgrade wizards prefill the saved name. Changing it does
not move existing data. See [Shared Folder permission](shared-folder.md).
Dedicated first installs require no folder parameter. To select an existing folder,
CLI deployment uses `--param media=Media` on first install, and
`--param CHANGE_media=true --param media=OtherMedia` to change it on upgrade.
Stop/disable and uninstall use DSM's official project worker; source data and
administrator-managed ACL entries are retained. A moved share needs a new
upgrade to resolve its volume path; start does not rematerialize the bind.

ACL changes affect subsequent filesystem operations. Already-open file
descriptors and data already read are not recalled. To stop existing streams,
stop the package before removing its access. This native route does not promise
automatic container termination or recreation on ACL changes.

For dynamic selection after install, declare the separate, blocked authority:

```yaml
compose:
  shares:
    media:
      select: admin-after-install
      permission: ro
      mounts: [{service: web, target: /media}]
```

Selection is after installation by an administrator in the App, not a claim
that the installation wizard can enumerate existing Shares. `install-draft`
is only a draft intent. Until authorization, only SDK settings start and no
upstream docker-project is acquired. The independent authority purpose is
`compose-mount`; a user destination grant from Download Hub/delegate never
permits a package-wide mount.

The dynamic implementation does **not** issue a mount grant, invoke ACL tools,
install a protected executor for Compose, or activate/revoke an upstream project.
Confirmation returns `COMPOSE_PROJECT_CONTROL_UNAVAILABLE`. It must remain
unsupported until an official ACL plus fixed project-control adapter can use
protected policy/digest/generation authority, stop/recreate/read back and revoke
without reviving stale grants. No app runs as host root to bypass this boundary.

| Acceptance blocker | Native install selection |
| --- | --- |
| `DSM_DOCKER_PROJECT_MODEL` | Uses the existing official docker-project worker; device acceptance still applies. |
| `DSM_STORAGE_OWNERSHIP_TIMING` | Existing shares need no package-created directory or chown; private package binds remain unsupported. |
| `DSM_EGRESS_BOUNDARY` | Unchanged independent network acceptance condition. |
| `COMPOSE_PROJECT_CONTROL_UNAVAILABLE` | Bypassed through native install/upgrade/stop/uninstall, without a dynamic controller. |
| `DSM_MOUNT_ACL_REVOKE` | Native kernel ACL semantics above; immediate stream cancellation remains outside this route. |

The first three general device acceptance labels remain in the plan. Dynamic
`admin-after-install` and `install-draft` keep all five blockers and
`mounts_blocked: true`; mixing dynamic authority with native selection is refused.

## Gateway transport

`none` keeps native app login; `header`, `token` and embedded `oidc` use supported
upstream interfaces plus the DSM account bridge. Access defaults to dsm-users;
admins/groups revalidate NSS membership per request. Public access is explicit
and only valid with none. Reserved DSM id/did and this gateway's cookies are
filtered in both directions; other Cookie order/duplicates and Set-Cookie
attributes are preserved. Claimed identity and DSM token headers are stripped.

Upstream POST does not require SynoToken. Browser mutations and WebSocket upgrades
require exact scheme/host/port Origin, or Referer when Origin is absent, plus
SameSite gateway cookies. Missing/null/foreign/duplicate origin fails. Explicit
app-authenticated passthrough can serve native clients without browser headers;
when browser evidence is present it is still checked. No identity is injected
into passthrough. SynoToken remains required at DSM's own authorization CGI.

WebSocket frames, SSE flush, streamed uploads, Range/HEAD downloads and redirects
have transport tests. Uploads default to 16 GiB; max_upload_bytes can reduce it.
Known oversize bodies are rejected before proxying; streamed oversize errors
return 413. Dial, TLS and response-header deadlines are separate; there is no
whole-response streaming timeout. Exact upstream absolute redirects change
origin while retaining encoded path, query and fragment; unrelated IdP URLs and
relative redirects keep their semantics. No HTML/JS body regex rewriting.

Protected WebSocket upgrades retain their gateway-session identity. While open,
they revalidate the gateway session, UID-to-name mapping and current admission
groups every five seconds; lookups have a two-second deadline and fail closed.
Expiry, logout, `Front.RevokeUID`, deleted/renamed accounts and loss of required
group/admin membership close both proxy connections within at most seven seconds
of the invalid state becoming visible to the configured directory. The transport
is closed without injecting application WebSocket frames (clients may see 1006).
Both socket ends close even when a client stops reading and the proxy write blocks.
This is the gateway session's TTL; native DSM logout has no separate push channel
and does not revoke an otherwise live gateway session unless the integration
calls `RevokeUID`. Public access, app-authenticated passthrough and OIDC-only
traffic without gateway admission retain their upstream authentication semantics.
Already-delivered frames and completed App side effects are not rolled back.
Header/token streams also close when the current administrator role differs
from the signed-in subject, even if `dsm-users` admission still permits the
account. Custom `FrontConfig` consumers that inject roles must provide live
`Membership` lookup; missing or failed lookup closes the upgrade. Membership
changes that retain admission and the injected role do not revoke the stream.

## Device acceptance remains required

A Linux gate is not DSM proof. Confirm final SPK digest and DSM/Container Manager
versions, worker interpolation/model semantics, storage ownership timing,
declared availability readiness, LAN isolation/egress, protected grant lifecycle, encrypted Share
ACLs, two-user visibility and clean install/upgrade/reboot/stop/uninstall before
calling the package supported on a NAS. No NAS is accessed by the build gate.

The unified Compose contract uses SDK/templates 0.27.0 with CLI 0.27.0. Use these published, matching versions together. DSM bridge ingress uses a dedicated internal network without published container ports.

Primary override files, and each included override set, must share a directory. Cross-directory override bases are rejected before loader I/O. Included implicit .env files are confined, inventoried and preserved; symlinks are rejected.

## DSM availability readiness

### Host networking

Declare optional non-loopback UDP listeners explicitly:

```yaml
compose:
  host_udp:
    - {port: 5353, purpose: mdns}
    - {port: 1900, purpose: ssdp}
  # Retain the verified host service, loopback listener and Web health policy.
```

This requires the existing nonroot host Web topology. Up to 32 distinct UDP
ports from 1024 through 65535 are allowed; privileged ports, ranges and wildcard
exceptions are rejected. Each purpose is a lowercase label of 1–64 characters,
beginning with a letter, followed by letters, digits or hyphens. The list is
sorted in the plan and lock and carried into daemon policy. `inspect` reports
the allowed ports and purposes and verifies the packaged policy against the
reconstructed plan. Use a CLI and SDK that both support this declaration;
older planners fail closed.

The exception applies to the whole package UID, to IPv4/IPv6 UDP only. Declared
sockets are optional and do not satisfy Web TCP readiness. Undeclared
non-loopback TCP/UDP listeners retain the existing rejection behavior; TCP Web
binding and health checks remain required. No root or added capability is
requested. This does not reserve ports, configure multicast reuse, prove LAN
discovery, inspect packet content or isolate host networking. Verify upstream
multicast coexistence without changing DSM discovery services. See the
[security boundaries](host-udp-design.md).

Host networking retains the package-UID and loopback binding guard. When
`compose.web.health` declares a relative path and an expected 2xx status, the
gateway also waits for that exact HTTP response. Redirects are not followed.
A temporary startup listener returning an unready status, including a gap before
the final server binds, cannot admit the gateway. The existing startup deadline
bounds the wait; extending it cannot make an unready response count as ready.

After admission, the supervisor continues probing health and listener ownership.
A missing listener or invalid binding remains fatal; temporary HTTP failures
close ingress and receive the bounded health recovery window, which equals the
startup budget. Host
profiles without a health declaration retain listener-only readiness. Rebuild
with matching CLI and SDK changes so the health declaration reaches the daemon.
Host mode does not require the bridge lifecycle-generation file. These checks
provide availability and binding evidence, not Docker identity attestation.

### Startup budget

For slow cold starts or upstream migrations, declare a finite budget in seconds:

```yaml
compose:
  startup_timeout_seconds: 600
  # Keep the actual files, services, network and web.health declarations.
```

The default is 900; valid values are integers from 2 through 900. Keep the
default unless device evidence supports a shorter value: on a loaded HDD NAS
with 2 GB RAM a cold start can take more than ten minutes. Use a CLI and
SDK that both support this field, then rebuild the SPK. The SDK plan supplies
the same value to the generated daemon start timeout and to every upstream
service as `SYNOPKGLAND_STARTUP_BUDGET_SECONDS`. This SDK-owned environment value
overrides upstream/env-file/service-policy values. A custom bootstrap adapter
must read it once at process startup and share one deadline across connection,
migration/setup and serving restart; do not restart the full timer at each phase.
For example, a Node adapter can use:

```js
const seconds = Number(process.env.SYNOPKGLAND_STARTUP_BUDGET_SECONDS);
if (!Number.isInteger(seconds) || seconds < 2 || seconds > 900) throw new Error('invalid startup budget');
const deadline = performance.now() + seconds * 1000;
const remaining = () => Math.max(0, deadline - performance.now());
// Give each existing bootstrap operation remaining(), and fail closed at zero.
```

An unmodified upstream may ignore this variable. The SDK does not rewrite an
App's custom bootstrap code or extend its existing hardcoded timers. That adapter
must adopt the environment contract when the App is rebuilt. Its health endpoint
must stay unready until all private bootstrap work is complete.

The package-account supervisor starts the gateway only after readiness succeeds;
it reserves five additional seconds for handoff, a bounded in-flight probe and
process stability. Until then no gateway forwards traffic. A timeout returns
`COMPOSE_STARTUP_TIMEOUT` and a fixed last `phase` code, also recorded in
`daemon/daemon.log`; it never copies probe URLs, bodies, credentials or raw errors.
Cancellation stops the wait. Stop/uninstall keep their independent bounds.

This budget begins after DSM's Container Manager startup worker returns. It does
not lengthen image pulls or the worker's own acquisition timeout. DSM's public
developer guide defines worker-before-start ordering but no universal time limit.
The examined package-manager source generates a 3600-second package-unit start
limit; the maximum SDK wait is 905 seconds including handoff. Platform overrides
and actual worker/Package Center limits still require device acceptance. The
gateway currently cannot show a starting page during this blocking wait.

Declare `compose.web.health: {path: /health, status: 200}` using the App's real
unauthenticated health endpoint (an origin-relative path and expected 2xx status).
The SDK follows no redirects, uses no proxy/cookies/DSM credentials, and bounds
TCP/HTTP timeouts. All three conditions are required before gateway admission:

1. The declared App health endpoint returns the expected status.
2. The planner-derived fixed bridge IP and container port accept a TCP connection.
3. A private, durably written `pkgvar/daemon/compose-acquire.json` records an active
   lifecycle acquire generation, the current installed SPK version and plan digest.

DSM's startup worker returns before the generated start hook records acquire.
Stop/upgrade/uninstall durably release it before stopping the gateway; a new
start records a fresh generation. Missing, released, foreign or stale state
fails closed. This is the package lifecycle's record, not DSM runtime readback.

**Limitation:** these checks cannot prove that the Docker image, project or
generation actually running is the declared one. The threat is another local
process occupying the same port and answering health. This is accepted because
that process must already have code execution on the NAS. Do not use readiness
as an identity or isolation attestation. Restore full authoritative verification
when an official package-account readback channel becomes available. Never grant
Docker socket access, borrow an admin session or use root for readiness.

`inspect` reports `compose.readiness.profile` and this limitation. The old
bridge-unverified admission blocker is replaced by `COMPOSE_READINESS_INVALID`,
`COMPOSE_ACQUIRE_GENERATION_INVALID`, `COMPOSE_TCP_NOT_READY` and
`COMPOSE_HEALTH_NOT_READY`, according to the actual failing condition.

### Healthcheck timing

Docker health is what Container Manager displays and what `depends_on:
condition: service_healthy` waits for. The plan keeps each App or image health
command and raises stricter timing to a slow-NAS floor: `timeout` 30s,
`retries` 3 and `start_period` equal to the startup budget. More tolerant values
are kept. `interval` is unchanged because Container Manager's Docker 24 has no
`start_interval`; a longer interval would only delay `service_healthy`. A service without a declared healthcheck gets the
same timing; Docker applies it to the image `HEALTHCHECK`, and an image without
one still has no healthcheck. `disable: true` and `test: [NONE]` are unchanged.
Do not declare healthcheck timing in an App; keep the command only. A command
with its own short limit (for example `curl --max-time 2`) is still stricter
than the floor, so remove such limits from the command.

Compose SPKs intentionally omit `install_dep_packages=ContainerManager`: DSM can
report that dependency not ready even while its systemd unit and Docker daemon
are active. The official `docker-project` worker checks actual availability;
this does not authorize starting, modifying or repairing Container Manager.

For disposable environments, optional `compose.resource_prefix: fixture-` prefixes
all generated project/network/volume names while retaining the package ID as the
namespace suffix. Keep it stable across upgrades: changing it selects new
resources and does not migrate or delete retained data.

Bridge web ingress uses a dedicated `sdk_ingress` internal bridge. The web service
receives a fixed IPv4 address, and the package-account gateway connects directly
to its declared container port. Internal bridges were reachable by package-account
hooks in DSM feasibility probes. No HAProxy helper, published
container port, Docker socket or host root is required. Explicit internet egress
still adds the existing outbound bridge; other App services retain their internal networks.

The subnet is a deterministic /29 candidate derived from package ID and
`compose.network.ingress_candidate` (integer 0-255, default 0). It comes from
the upper half of the RFC 1918 10/8 private address block; this pool is not globally reserved. Before acquisition, the
package-account preparer reads kernel IPv4 routes and all interface prefixes, and rejects overlap with
`COMPOSE_SUBNET_CONFLICT`. Choose another candidate and rebuild; no silent
renumbering occurs on upgrade. Choose a different candidate for a clean
installation; automatic migration of an installed network is not provided. The exact previous package allocation can be
reused through a private `pkgvar/compose-ingress.json` allocation record written
in postinst. Preinst reads DSM7's physical `<SYNOPKG_PKGDEST_VOL>/@appdata/<id>`
when `SYNOPKG_PKGVAR` is absent: DSM removes both the old payload and
`/var/packages/<id>/var` before replacement;
it does not attest Docker ownership. Missing or invalid history never exempts a
conflicting route. Container Manager's atomic IPAM check remains the final arbiter for
concurrent acquisition and networks without a visible host route; the preparer
does not claim a complete Docker network inventory or network ownership attestation.
Unavailable kernel inventory fails with `COMPOSE_SUBNET_INVENTORY_UNAVAILABLE`.

`inspect` inventories the address, subnet, service, port and candidate. The gateway
accepts only its package-derived bridge address; arbitrary private-network URLs,
DNS targets and user-supplied credentials/query strings are not ingress endpoints.
The readiness profile is `health-fixed-ip-lifecycle` and retains the existing
availability-only limitation. Host-network OIDC remains a separate explicit opt-in.

Bridge publish mode has been removed. Existing `web.host_port` declarations are
accepted for compatibility and ignored by bridge transport; new scaffolds omit it.
Legacy `.sc` residue is not purged by this topology. Migrating an already published
installation requires a separately authorized cleanup or an official worker fix;
a no-publish rebuild cannot claim to remove historical service registrations.

On nas.example DSM 7.4/Docker 24.0.2, internal networks blocked tested external IPv4
and external DNS, but allowed NAS host services through the bridge gateway and
NAS LAN address. Therefore `isolated` is not a host-service firewall. IPv6 had
no usable default route on this fixture, which is not proof of an IPv6 firewall.
The DSM docker-project provider adds NET_BIND_SERVICE to the capability bounding
set for Compose packages, including non-GPU packages. The SDK emits `cap_drop:
[ALL]` and never requests `cap_add`. With the nonroot package UID and
`no-new-privileges`, the tested process has zero effective, permitted and ambient
capabilities; the remaining bounding set is `0x400`. This is general DSM platform
behavior, not a GPU acceptance blocker. Do not claim an empty bounding set or
complete NAS isolation.

DSM cookie names `id`, `did`, `_SSID`, `_CrPoSt`, `stay_login` and the complete
`synopkgland_sso_` namespace are reserved in both request Cookie and response
Set-Cookie. Other App sessions/native cookies retain their normal semantics.
Cookies are shared across ports on the same hostname, so filtering only this
package's SDK cookie would leak another App's SDK session to an upstream.

Historical publish-topology nas.example validation was release-blocked: the Container Manager
24.0.2-1706 docker-project worker can return profile error 1202 on update,
which Package Center reports as postinst-worker failure 277. The owned test
project and a real Vaultwarden package reproduced this; switching Compose
publish syntax did not resolve it. No root App, firewall edit or Container
Manager modification is used as a workaround. Validate acquisition, upgrade
and uninstall on a working official worker before claiming DSM support.

### Diagnose official worker failures without changing the NAS

Package Center's postinst-worker error is a wrapper. Read only the failed
package's worker messages and the matching timestamp/PID in DSM logs, removing
session, token, cookie and password fields before displaying or saving output.
Distinguish these observed failure chains:

| Worker result | Matching lower-level evidence | Interpretation |
| --- | --- | --- |
| Profile 1202, Package Center 277 | `service_conf_section_set.c:92 Port conflict with existing ports` followed by `profile.cpp:118 Fail to create port section` | Official service-port registration rejected a conflict; malformed Compose syntax is not established by these codes |
| Compose-up 2201, Package Center 276 | Registry request fails with `x509: certificate signed by unknown authority` | The Docker daemon could not fetch an image; gateway authentication and readiness have not been tested yet |

For the first chain, `servicetool --conf-port-conflict-check --tcp PORT` and
`servicetool --runtime-port-conflict-check --tcp PORT` are read-only checks.
Read the matching lower-level error even for wrapper 276: a follow-up failed
reinstall returned 276 with profile 1202, rather than a registry failure.
Record the reported service/process owner. A clean readback after uninstall
does not identify the conflict that existed during the failed operation.
Do not remove service profiles, disable firewall checks, bypass the official
worker, change ports automatically or restart Container Manager to turn the
failure into a pass.

Container Manager's status script also requires `synosystemctl` on PATH. A
status failure from a restricted SSH environment alone does not prove daemon
failure; correlate the script's actual error, service-unit state and Docker
server response. Keep administrative diagnostics separate from the App, which
continues to run as its package account without Docker socket access.

An additional DSM 7.3.2 / Container Manager 24.0.2-1606 fixture passed Header
and Vaultwarden install/start/upgrade. This does not certify the failing DSM
7.4 / 24.0.2-1706 environment. Its explicit test configuration used immutable
mirror image references and declared internet egress for the web service; it
does not extend the isolated-ingress or original-registry support evidence.
The same Header SPK also passed install/start/upgrade on a second DSM 7.4 /
24.0.2-1706 device. The original failure therefore cannot be classified as
affecting every device with that Container Manager version.

Official uninstall on both additional fixtures removed the packages, projects,
containers and networks but left their `docker_<container>.sc` service-port
profiles. Read-only configuration checks still reported those ports as reserved
after the containers were gone. This is a separate cleanup acceptance failure;
successful upgrade/reinstall does not clear it. The retained profiles are a
possible source of later port conflicts, not proof of the original conflict's
owner. Do not add a root App hook or delete DSM service configuration as a
workaround; record the owned profiles and resolve the official worker cleanup
contract before claiming complete uninstall support.

### Official port-profile cleanup boundary

Follow-up tests isolated the uninstall defect from SDK-generated ingress. Minimal
packages containing only the official docker-project resource, package-account
hooks and a non-root Node container reproduced retained port reservations with
all three forms: long loopback publish, short loopback publish and wildcard
publish. Distinct project names and distinct ports allowed two Apps to run
concurrently, but did not make their uninstall clean. A no-publish control did
not create a port-profile `.sc`. Changing syntax or exposing the port to the LAN
is therefore not a validated cleanup fix.

Read-only inspection of Container Manager 24.0.2-1606 and 1706 found that
Project.Clean calls Compose.Down before RemoveProfileForAllContainers; that
cleanup method obtains IDs through Compose.Ps `-qa`. Once Down has removed the
containers, that lookup can produce no profiles to unregister. The observed
files were created during install acquisition, before the package was started.
This supports an official worker cleanup defect; it does not establish the
exact internal conflict owner or race during a separate failed upgrade.

The documented [port-config worker](https://help.synology.com/developer-guide/resource_acquisition/port_config.html)
was also tested. Even when its protocol-file basename and section matched the
CM file, DSM registered it as `<package>.sc`. Container Manager separately
registered `docker_<container>.sc`; releasing port-config removed only the
package registration. Declaring that resource did not solve the CM residue.
The [docker-project contract](https://help.synology.com/developer-guide/resource_acquisition/docker-project.html)
documents project deletion on PREUNINST and build flags, without a separate
package-account profile-release option.

The original isolated Header fixture on nas.example installed and started from a
clean configuration, then its upgrade still returned port conflict → 1202 →
277. Historical `.sc` residue alone is therefore insufficient to explain or
resolve that environment's upgrade failure. Preserve the timestamp/PID chain;
do not report a clean post-uninstall check as proof that upgrade was fixed.

The publish topology did not pass the complete lifecycle. The no-publish
implementation above requires its own final-SPK install/upgrade/reinstall/uninstall
and two-App acceptance before release. An administrator may separately authorize exact
owned-profile cleanup through official servicetool, with pre-removal hashes and
file/config readback; this is fixture recovery, never an App hook or a supported
rootless package cleanup mechanism.

A native package-account listener remains another rootless option. The new
no-publish bridge topology above implements the separately authorized ingress design. Existing host-network OIDC support requires a
verified loopback address setting, cannot remap ports or enforce isolated
egress, and is not a general replacement for Header/native-cookie bridge Apps.
Do not silently switch networking, add Docker socket access, run an App hook as
root or delete DSM configuration to claim compatibility.

### Registry check messages

Rebuilt container packages run `compose/control diagnose --project <packaged-compose-dir>
--phase <preinst|postinst|postupgrade|prestart>` from their existing package-account
hooks. Checks are informational, including preinst; a registry outage does not
block a cached image from starting. After an App first passes readiness, its
supervisor records the locked image references in the private
`pkgvar/daemon/compose-images.json`. A registry whose locked images are all
recorded is not contacted, so an offline NAS shows no registry message on Start
or on an upgrade that keeps the same image digests. A new digest is still checked.
The record is lifecycle evidence, not Docker image-store readback: if an
administrator deletes an image, Start fails as before and Repair downloads it. Existing Container Manager availability and
Compose preparation failures keep their native exit status.

The command reads immutable references from `compose.lock.json`: at most
64 images and four distinct HTTPS registry hosts, two seconds per request and
five seconds total. Docker Hub maps to `registry-1.docker.io`. It requests only
`/v2/`, follows no redirects or authentication token realms, reads no response
bodies and inherits neither Docker credentials nor environment proxy settings.
For Docker Hub, it also reads `registry-mirrors` from the first available daemon
configuration: `/var/packages/ContainerManager/etc/dockerd.json`, legacy
`/var/packages/Docker/etc/dockerd.json`, then `/etc/docker/daemon.json`. The read is
limited to 256 KiB and rejects symlink/nonregular files. Up to four distinct mirror
origins are checked alongside the upstream within the same total budget; blocked
paths cannot delay a successful mirror result. Configured HTTP mirrors are
supported; URL credentials, paths, queries and fragments are rejected. Other
registries do not use Docker Hub mirrors, as specified by
[Docker's mirror configuration](https://docs.docker.com/docker-hub/image-library/mirror/).
Unreadable/invalid configuration contributes no mirror endpoints; no Docker socket
access or elevated permissions are added.

200 and the normal anonymous 401 challenge count as reachable. If any applicable
path is reachable, the registry produces no warning. If all paths fail, the
upstream probe result is reported without mirror URLs or response text. A 403 on
`/v2/` reports an unknown cause, never `COMPOSE_IMAGE_DENIED`: a firewall can deny
this probe while Docker can still obtain the image. A 404 does not establish that
an image is missing. A successful check does not prove
that the daemon can authenticate, download layers, or use the same CA/proxy path.

JSON diagnostics have schema 1, `code`, `cause`, `message_key`, `source`, `phase`,
`registry_host`, `observed_at`, `retryable` and `sanitized_detail`, with an optional
`http_status`. Causes are `tls_trust`, `unreachable`, `dns`, `rate_limit`,
`disk_full`, `not_found`, `denied` and `unknown`. Registry checks use source
`registry_check`; the separate native-failure adapter uses `container_manager`
only for known lower-level failure text. Wrapper codes never identify a cause.
The read-only check does not access worker logs or the Docker socket and cannot
diagnose the daemon's disk space or an image manifest from a registry-base check.

The CLI packages `diagnosis-messages.json` from its existing nine-language
catalog. The command appends native text to `SYNOPKG_TEMP_LOGFILE`, preserving
existing script text, and prints structured JSON to stdout. Probe text is
labelled **Registry check** and ends with the native recovery instruction: fix
the cause, then use Package Center **Repair**, which re-runs the install worker
and downloads missing images. Repair remains DSM's install/repair operation;
there is no SDK Retry action, deferred acquisition, credential store or new
privileged helper. No check changes CA, proxy, DNS, containers, images or data.

A failed worker can leave a broken package and roll back its project. Native
Start only starts existing containers and cannot repair that installation. Use
the same package identity and preserve app data and named volumes when repairing;
verify the package's own migration and uninstall scripts separately. This
feature does not guarantee arbitrary third-party App hooks preserve data.

## Intel GPU render devices

GPU support is an opt-in and requires DSM's native `video-driver` resource.
The runtime defers the discovered constant group ID until Compose interpolation,
so DSM's intermediate YAML rewrite cannot turn a numeric group string into an
integer rejected by its Compose version. The manifest cannot supply that ID.
Native resource grant timing and a real hardware transcode require device
acceptance; a successful build does not prove hardware acceleration works.
The general DSM provider capability behavior above also applies to GPU packages.

```yaml
compose:
  files: [compose.yaml]
  devices:
    gpu:
      services: [media]
```

The SDK plans this capability without probing the developer's machine. `inspect`
reports the selected services, `video-driver` resource, native `videodriver` GID
source, runtime render path selection, and software fallback. The package-account
installer detects Intel DRM render nodes using sysfs vendor `0x8086`, character
device major 226/minor 128–191, and a matching `videodriver` device GID with group
read/write access. It maps each accepted `/dev/dri/renderD*` node individually,
with `rw` access, and adds only the discovered numeric GID to selected containers.
It does not map `/dev/dri` as a directory or expose `card*` nodes.

The CLI emits the native resource. DSM owns group authorization; the SDK never
changes group membership, device ownership or mode. No Docker socket, root hook,
`privileged`, `cap_add`, arbitrary path or manifest-supplied GID is accepted. GPU
services keep the package UID:GID, drop all capabilities and retain
`no-new-privileges`. The package-account materializer verifies its native account
and a hash-bound `gpu.template.yaml` before writing the worker document.

A missing or unsupported GPU, inaccessible discovery metadata, or mismatched GID
produces no device/group mapping. Containers receive
`SYNOPKGLAND_GPU_STATUS=software` and an empty `SYNOPKGLAND_GPU_DEVICE`. A mapped
render device produces `available` and the first accepted device path. These
values describe device mapping, not a proven codec/driver; the App must probe its
upstream encoder and select software if initialization fails. Reinstallation or
upgrade recomputes mappings; hotplug or changing hardware without rematerializing
the package is unsupported. GPU access authorizes shared render-engine ioctls; it
does not isolate GPU workloads or imply per-DSM-user file authority.
