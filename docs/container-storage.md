# Official registry images and nonroot container storage

Use the upstream official registry image as the only image source. Declare it
in the upstream Compose service's `image` field. The build resolves and verifies
its Linux platform manifest/config and locks the digest in the effective worker
Compose document. Container Manager downloads that immutable image at install.
The SPK carries SDK code and image metadata, without image layers. Verify the
publisher's documented repository; the SDK cannot infer which publisher is
"official" for an arbitrary App. Init and examples retain this registry route.

The planner's package UID policy does not make an official image's fixed-UID
data directory writable. A named volume copies the image directory's existing
ownership; a UID-1000 directory with mode 0755 still rejects a different package
UID. This problem also exists with an unmodified upstream official image. A
successful manifest check/build is not a writable-storage proof.

Declare `compose.services.<service>.storage_init` to run the SDK initializer as
the package UID before the App. The CLI builds the initializer for the SPK's
architecture, packages it as `compose/storage-init`, and the SDK mounts that
single executable read-only at `/synopkgland/storage-init`. The source is the
installed `/var/packages/<package>/target/compose/storage-init`; it is available
from the SPK payload, independently of pkgvar creation. Docker must not create a
missing source path. The official image stays unchanged: no derived image,
image-baked helper, runtime compiler, shell, root or Docker socket is needed.
DSM worker timing and executable-bind behavior still require device acceptance.

Example using the verified upstream Uptime Kuma 2.5.6 slim-rootless image:

```yaml
services:
  web:
    image: ghcr.io/louislam/uptime-kuma@sha256:b869b91cc5af9923f9c20ff8d39c525587d55245d4bbe1e9a75913ba873f5f8a
    entrypoint: ["/usr/bin/dumb-init", "--"]
    command: ["node", "server/server.js"]
    environment:
      DATA_DIR: /tmp/package/data
      UPTIME_KUMA_DB_TYPE: sqlite
    volumes: ["state:/tmp"]
volumes:
  state: {}
```

Package declaration:

```yaml
compose:
  services:
    web:
      storage_init:
        directories:
          - {base: /tmp, path: package/data}
```

Choose an existing writable mount base in the **official image**, for example
its existing sticky temporary directory mounted as a persistent named volume.
A missing mount target is normally created root-owned; declaring it does not
make it writable. The SDK does not chown/chmod the mount base. Verify its initial
copy-up contents and avoid hiding required App files. A tmpfs base is ephemeral
and is unsuitable for persistent databases. If the official image offers no
compatible writable base or configurable data path, keep it unsupported; do not
add a root initialization container or silently switch to a custom image.

For example, Uptime Kuma can use an official image's persistent named volume at
`/tmp` and point `DATA_DIR` to `/tmp/package/data`. Declare `{base: /tmp,
path: package/data}`. Explicitly use its supported nonroot App executable/argv
instead of an upstream entrypoint that requires root. Confirm actual health,
UID, capabilities and persistent database state for the locked image.

The initializer creates only the declared private subtree and parents, with
mode 0700, using file-descriptor traversal without symlinks. Shared writable
bases require the sticky bit. On every start it verifies package ownership and
private modes, creates an exclusive probe, writes, syncs and removes it.
Missing/unwritable bases, foreign ownership, symlinks, broad modes, read-only
storage, quota and full-disk errors fail with `COMPOSE_STORAGE_NOT_WRITABLE`.
The App is not executed. Existing data and permissions are preserved across
restart and upgrade; there is no ownership or permission repair.

Declare the upstream `entrypoint` explicitly (including `entrypoint: []` when
`command` contains the full executable argv). Inherited image defaults are not
guessed. The initializer passes the original argv literally, then uses exec so
the App retains PID, signals and exit status. It sets umask 077. The declaration
and SDK-owned read-only mount are inventoried in the lock and effective-plan
digest. Check/build reject malformed declarations, read-only/unmounted bases,
missing explicit argv, collisions with the SDK executable mount and
container-root services with `COMPOSE_STORAGE_INIT_INVALID`. Inspect verifies
that the SPK carries an executable binary for the requested architecture and
rejects a removed initializer or modified worker that bypasses preflight.

Registry manifest/config metadata cannot prove writable runtime ownership. The
actual runtime probe is authoritative. An image- or package-UID change needs an
explicit data migration plan; the SDK does not rewrite old UID-1000 data or
migrate existing App data paths.
