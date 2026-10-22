# Private CI

The controller polls an explicit list of private repositories every 30 seconds.
It creates one repository-scoped JIT runner per job, with global concurrency one.
It checks repository visibility again immediately before registration and while
a job is running. Public repositories cannot be provisioned. The Docker network
is dedicated to CI and disables inter-container communication.

Only the controller has access to the host Docker socket and the root-owned
token file. Jobs run as `runner` (UID 1001), with no host mounts, no capabilities,
no privilege escalation and no published ports. They receive a single-use JIT
configuration through a file, which is removed before the runner starts.
Containers and their writable layers are removed after a job exits, after five
minutes idle, or after one hour maximum. Only the three bounded cache volumes
persist; source workspaces and complete target trees remain disposable.

Limits: controller 0.5 CPU / 256 MiB / 64 PIDs; runner 2 CPUs / 4 GiB / 512 PIDs,
with swap disabled and capped local logs. Controller restart policy is
`unless-stopped`; it reconciles surviving jobs and orphan registrations after
restart. Only its ownership label and exact runner name are eligible for cleanup.

Install the code in `/opt/ci-private` on a Linux Docker host. Supply the controller
PAT in `/etc/ci-private/github_token`, owned by root with mode 0600 and parent
directory mode 0700. Do not use environment variables or command arguments for
the PAT. Then run `bash deploy.sh` as root. The script builds images, checks the
secret permissions, creates the dedicated network and starts the controller.
It does not alter unrelated containers, volumes, caches or system services.

The least-privilege fine-grained PAT selects only the private repositories listed
in `controller.py`, with Administration write, Actions read, Contents read and
Metadata read. Contents read is needed to transfer private SDK/template source
without exposing a credential to the job.
No job receives this PAT. An existing broader PAT should be replaced with this
scoped credential when available. Never commit tokens or JIT configurations.

Private jobs receive a Git bundle of the SDK (except SDK jobs themselves); CLI
and template jobs also receive the templates bundle in `/home/runner/deps`.
The controller clones mirrors using a credential helper
that reads its root-only secret; no PAT appears in argv, env, logs or bundles.
The job restores real source checkouts with their original Git refs/tags, which
the existing Rust gates require. Bundles are limited to 64 MiB each and only two
fixed repository names are allowed. The controller uses a 256 MiB temporary
filesystem for these mirrors. It includes no credential/config files in bundles.
Go and Cargo use a local Git URL rewrite to resolve the original pinned tags
without a credential in the job. The restored default branch supports CLI/template
local integration gates; it does not replace
pinned release certification. Fetching source can add startup latency.

The deployment copies the host's CA trust bundle for HTTPS inspection without
disabling TLS validation. The private image uses the public npm mirror at
`registry.npmmirror.com` because direct npm registry access is blocked on this
network; lockfile integrity checks remain enabled. No registry credential is
included. Images contain public toolchains, public trust certificates and the
runner binary verified against its pinned SHA-256.

Private workflows use `[self-hosted, private-ci]`. Docker-dependent, release,
publishing and full cross-architecture checks stay manual; no host socket or
privileged DinD is available. `sdk-fast.sh` reuses the SDK path selector and native
toolchain checks. Old-runtime containers, cross-architecture and reproducibility
checks stay in the complete release gate. This is intentionally a narrower CI
boundary than release certification. Do not treat green CI as release approval.

Operational limits: containers share the host kernel; the dedicated bridge is
not a hostile-code VM sandbox. Jobs can reach routable network services. This
setup accepts only trusted private code. Resource limits do not enforce a disk
quota on the writable layer; monitor free space. Generic names prevent local
host details from being injected into logs, but cannot hide the public egress IP
from a program that explicitly queries an external service.

Check `docker logs ci-controller`, `docker ps -a --filter label=ci.managed=private-ci-v1`
and GitHub job logs. Restart only `ci-controller` to verify recovery. To uninstall,
stop the controller, wait for its one active job to finish, remove only managed
job containers/runner registrations, then remove `ci-controller`, `ci-jobs`, the
two `ci-` images and the two installation directories. Do not prune Docker.

## Inherited workflow policy

Never rename, edit or remove upstream files to disable automation. Add only the
uniquely named package workflow and `synology/docs/ci.md` to upstream copies.
Use `workflow-policy.py OWNER/REPO --token-file /secure/operator-token --mode prepare`
to disable every registered workflow except the exact owned path, while repository
Actions remains disabled. The dedicated operator token needs Actions write,
Administration write, Contents read and Metadata read; do not give it to jobs or
replace the narrower runtime credential with it.

After the reviewed PR merges, run `--mode activate`. It checks unfinished runs,
sets public fork PR approval to `all_external_contributors`, disables inherited
workflows **before** enabling repository Actions, verifies all default-branch
workflow files are registered, then reads back the one active owned workflow.
Any failure rolls back to repository Actions off. For catalog/community the
existing owned path is `.github/workflows/fast-ci.yml`; pass `--workflow` explicitly.

Before **every** upstream tag merge, run `--mode pause`, drain unfinished runs,
merge the tag through the existing upgrade procedure, and rerun `--mode activate`
before further push/PR activity. New inherited workflows must never get a live
activation window. The tool refuses activation on an unregistered workflow.

GitHub's exact generated path `dynamic/dependabot/dependabot-updates` is the
single permitted exception. GitHub manages it independently of repository
Actions, and its workflow-disable API may return HTTP 422. Never attempt to
disable it or change upstream dependabot.yml. Its state is explicitly included
in `allowed_exceptions` and the full workflow readback; its unfinished runs do
not block activation. Every other inherited workflow must be disabled, and our
owned workflow must be active. Similarly named YAML/dynamic workflows receive
no exemption. This does not change Dependabot/security settings.

API contracts: [workflow disable/enable](https://docs.github.com/en/rest/actions/workflows),
[repository Actions and fork PR approval](https://docs.github.com/en/rest/actions/permissions).

## Bounded shared caches (private jobs only)

Three named volumes are exclusive to this CI controller: `ci-cache-go` (module
and build caches), `ci-cache-cargo` (registry/git sources and sccache objects),
and `ci-cache-npm` (npm download cache). UID 1001 can read/write them. Job source,
Cargo config/credentials, complete `target/` trees and workspaces remain ephemeral.
Rust uses pinned sccache 0.18.0 with incremental compilation disabled; its local
object store has an additional 6 GB limit. npm lock integrity remains enabled.

Before/after jobs and on every idle poll the controller starts a networkless,
read-only helper with only these volumes. Above **15 GiB allocated disk blocks**,
it evicts least-recently-used units (mtime/atime) to **80%**, then reads disk
usage back. Complete module/source/index/checkout entries are evicted together.
Go build files not accessed or modified in three days are also removed. A trim
failure prevents dispatch. This is a boundary-based soft limit: an active job can
temporarily exceed it; it is pruned after retirement, never while a CI job uses it.

The same controller runs a separate daily helper after startup and every 24 hours
when CI is idle. Its **only** mount is the existing `synopkgland-cache-gobuild`:
remove regular files with mtime older than three days, without following symlinks.
It does not change other thread volumes, images or projects. No systemd timer
is installed. A restart can repeat the harmless age check earlier than 24 hours.

Caches may be poisoned by another trusted private repository of the same owner;
this tradeoff is accepted. Public repositories are excluded at both controller
routing and Docker launch. Named caches contain no controller PAT or host mount;
only the controller has the Docker socket. Delete only these three `ci-cache-*`
volumes after stopping/draining CI to reset the shared cache.

`cache-probe.py` compiles this repo's Go/Rust code and installs locked npm
packages. The SDK workflow runs it for infrastructure changes; manual dispatch
can select `cache_probe` after the workflow is merged. Compare its `CACHE_PROBE`
JSON and sccache statistics on two fresh jobs at the same commit. A warm cache
is a performance aid, not product or release certification.
