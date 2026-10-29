# Preparing npm releases

Build and gate the package before packing it. Prepare the gated archive in a new
private staging directory using `python3 scripts/prepare-npm-publish.py ARCHIVE DESTINATION`.
The command rejects unsafe archive members and local dependency references,
removes installation-only manifest fields, and verifies that repacking preserves
every gated file's bytes except the normalized package manifest.

Scan the resulting archive with the repository content scanner and the independent
publication audit scanner. Supply the private policy through `SPL_INTERNAL_DENYLIST`;
never commit policy contents. Resolve all findings and scanner errors before release.

When publication is authorized, use the directory-only wrapper:

```sh
python3 scripts/publish-npm.py PACKAGE_DIRECTORY --userconfig PRIVATE_NPMRC
```

Pass the exact `packageDirectory` returned by preparation. The wrapper rejects
tarball paths and invokes `npm publish --ignore-scripts --access public` with
the prepared directory as its current directory and no package argument.
Keep the temporary npm userconfig outside that directory and delete it afterwards.
Never pass any archive pathname to `npm publish`, including the cleaned archive:
npm can copy that pathname into public `_resolved` and `_from` metadata.

After publication, save the full `npm view PACKAGE@VERSION --json` output and the
raw JSON response from `https://registry.npmjs.org/PACKAGE/VERSION`.
Validate both with `python3 scripts/check-npm-metadata.py DOCUMENT...` and the
independent audit scanner. Check the published tarball integrity and distribution
mirror separately. A version or integrity match alone does not establish clean metadata.

`python3 scripts/test-npm-publish.py` captures real npm requests against a loopback
registry. The old archive-path request must fail metadata validation; the directory
request must pass and upload exactly the scanned repacked archive bytes. This test
also verifies that the wrapper rejects tarball paths before running npm. It never
contacts or modifies a public registry.

## Executor manifest contract

SDK releases carry `executor-manifest.json` at the Go module root and in the
frontend npm package (`@synopackageland/sdk/executor-manifest.json`). Both files
must be byte-identical. Format 1 declares `sdkVersion`, `executorVersion` and
`executorDigests`: SHA-256 pins for executor and lifecycle on `x86_64`, `aarch64`
and `armv7`, plus the exact `goToolchain`. Format 1 uses static Linux builds,
`-trimpath -buildvcs=false -ldflags '-s -w -buildid='`, CGO disabled,
GOAMD64=v1 and GOARM64=v8.0 (GOARM=7 for armv7). Changing that build contract
requires a new manifest format and corresponding CLI support.

Before packing a release:

1. Record the reproducible results from `bash scripts/executor-digests.sh VERSION`
   in `release/vVERSION.json`, together with the immutable `executorVersion`.
   Reusing an older executor tag requires identical executor source and bytes;
   otherwise create a new executor release. Never use a branch or `latest`.
2. Run `node scripts/executor-manifest.mjs` after updating package versions and
   the release record. Commit both generated files with the release metadata.
3. Run `node --test scripts/executor-manifest.test.mjs` and
   `node scripts/executor-manifest.mjs --check`. The full release gate also
   compares both reproducible builds with the declared six digests; stale pins
   fail. Inspect the actual npm tarball and Go module archive for the manifest.
4. Publish/tag only after release approval. Consumers lock npm integrity and Go
   module sums; the CLI reads the manifest from the resolved SDK module and
   verifies both built payloads before packaging. Nothing fetches a manifest
   at NAS runtime. A compatible SDK minor needs no CLI release. An unsupported
   format requires CLI support; malformed manifests are always rejected.

The manifest ships starting with SDK 0.27.1, which reuses executor/v0.27.0
and its six verified payload digests. When executor sources and build inputs are
unchanged, retain that immutable inventory and its existing reproducibility
evidence; do not rebuild an executor matrix or create another executor tag.
SDK 0.28.0 and 0.29.0 also reuse executor/v0.27.0: executor sources are unchanged since 0.27.0. SDK 0.26.0 and 0.27.0 as
already published remain compatible through the CLI's frozen legacy inventory.
