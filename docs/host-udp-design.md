# Explicit host UDP listeners

Host-network Compose Apps may declare `compose.host_udp: [{port: 5353,
purpose: mdns}, {port: 1900, purpose: ssdp}]`. This is a package-UID-wide
configuration guard exception, not a firewall or a service-specific grant.

| Boundary | Contract |
| --- | --- |
| Opt-in | Omission preserves the existing guard. Nonempty declarations require the existing verified nonroot host Web topology. Bridge and headless Apps cannot request this exception. |
| Exact ports | Up to 32 distinct integer UDP ports, 1024–65535 inclusive. No privileged-port exceptions, ranges, wildcards, inferred ports or ephemeral-port exemption. Purpose is a 1–64 character lowercase label beginning with a letter; it records intent, not packet inspection. |
| Protocol and ownership | Only unconnected UDP sockets owned by the package UID gain the declared non-loopback exception, on IPv4 and IPv6. Other UIDs remain outside this guard. Existing loopback and connected-socket behavior is preserved. |
| TCP | UDP declarations never admit TCP. The Web listener must still be owned by the package UID on 127.0.0.1 and pass declared HTTP health. Existing explicit gateway TCP ports are unchanged. |
| Privilege | No root, cap_add, Docker socket, sysctl, daemon configuration or DSM setting changes. Keep cap_drop ALL and no-new-privileges. Platform-added bounding capabilities are an observed platform property, not an SDK request or guarantee. |
| Artifacts | The planner validates and sorts declarations by port, returns the canonical list and includes it in the lock. The CLI rejects missing, changed or inconsistent planner/lock support, derives daemon policy from that list, and inspect rebuilds and compares the artifacts before showing ports and purposes. Old SDKs reject this new request field. |
| Runtime | Revalidate the declaration before scanning socket tables. A declared UDP socket is optional and cannot satisfy TCP readiness. An undeclared non-loopback listener still fails readiness and closes ingress under the existing lifecycle policy. |
| Multicast and conflicts | Declaration does not reserve a port, configure reuse, join groups, or prove LAN discovery. The upstream App must support safe coexistence. A bind conflict is an App failure; do not stop DSM discovery services or add privileges to fix it. |
| Outside scope | Host networking can reach NAS services and LAN peers. The guard samples state and does not stop the upstream container or prevent transient listeners. UDP content, destination filtering, per-service ownership, Companion clients and discovery support require separate runtime evidence. |

Validation covers planner rejection and canonical lock inventory, IPv4/IPv6
socket tables with TCP/UID negative controls, and CLI declaration→build→inspect
round trips including artifact mutation. Device evidence belongs to the App's
feasibility report, not to a universal SDK support claim.
