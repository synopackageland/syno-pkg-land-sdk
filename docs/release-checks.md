# Release checks

Use `npm run gates` (or `bash scripts/gate.sh`) for the basic release checks.
Use `npm run gates:full` (or `bash scripts/gate.sh --full`) only when explicitly
requested. `SPL_FULL_GATE=1` is an alias for `--full`. `--plan` prints the
workloads without executing them. `--fast` and `gates:fast` alias basic.
The root package is a private command dispatcher; the public npm packages
remain in `node/` and `ts/` and expose the same gate commands.

| Entry point | Default | Explicit full |
| --- | --- | --- |
| `scripts/gate.sh`, `npm run gates` | Policy/version/manifest unit checks, timeout-policy guard, native Go vet/build for root and executor, core API/DSM/File Station/JSON/identity units, Python units, Node typecheck/units/build, frontend typecheck/units/build, pack and content scans | Adds the retained integration gate: complete Go race suites including daemon/gateway and conformance, Rust checks and target clippy, Python/Node runtime containers, cross-architecture Go vet/build, two executor builds including empty GOCACHE, manifest `--verify-build`, loopback npm publication |
| `ops/private-ci/sdk-fast.sh`, CI | Basic | Private cache probe requires a manual dispatch input |
| `scripts/ts-gate.sh` | Frontend typecheck/units/build, local packed consumer import, package inventory and content scan | Same checks |
| Node/frontend `prepack` | Build and static manifest/version check | No integration checks |
| `scripts/executor-manifest.mjs --check` | Validate committed inventory and generated copies | `--verify-build` compares explicitly supplied build evidence |
| `scripts/executor-digests.sh`, `scripts/gate-full.sh` | Manual integration commands | Retain target digests and cold comparison |
| `scripts/prepare-npm-publish.py` | Safe archive extraction and byte-preserving repack | No integration checks |
| `scripts/publish-npm.py` | Metadata validation, directory publication with scripts disabled | No integration checks |
| `scripts/release-crate.sh` | Dry-run Cargo packaging/publication checks in a Linux container | `--publish` requires release authorization/tag; no implicit extended gate |
| Runtime/Docker/Rust helper scripts | Explicit manual integrations or build isolation | Invoked by extended checks; never required by basic release |

Build isolation using a configured Docker host does not enable integration
selection. Run Rust only inside a Linux container. Basic keeps the timeout-policy
guard and immutable executor manifest validation; it does not cold-rebuild
released executor bytes. Reuse an unchanged executor inventory, or generate a
new inventory explicitly when executor sources/build inputs change. The public
CLI still validates payload digests when building an application. No QEMU
workload is wired into the default SDK release path.
