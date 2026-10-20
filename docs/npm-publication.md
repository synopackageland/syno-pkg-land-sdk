# Preparing npm releases

Build and gate the package before packing it. Prepare the gated archive in a new
private staging directory using `python3 scripts/prepare-npm-publish.py ARCHIVE DESTINATION`.
The command rejects unsafe archive members and local dependency references,
removes installation-only manifest fields, and verifies that repacking preserves
every gated file's bytes except the normalized package manifest.

Scan the resulting archive with the repository content scanner and the independent
publication audit scanner. Supply the private policy through `SPL_INTERNAL_DENYLIST`;
never commit policy contents. Resolve all findings and scanner errors before release.

When publication is authorized, run `npm publish --ignore-scripts --access public`
with the prepared **package directory as the current directory**, and without a
tarball or `file:` argument. Keep the temporary npm userconfig outside that directory.
Do not publish the original archive pathname: npm can copy it into public metadata.

After publication, save the full `npm view PACKAGE@VERSION --json` output and the
raw JSON response from `https://registry.npmjs.org/PACKAGE/VERSION`.
Validate both with `python3 scripts/check-npm-metadata.py DOCUMENT...` and the
independent audit scanner. Check the published tarball integrity and distribution
mirror separately. A version or integrity match alone does not establish clean metadata.

`python3 scripts/test-npm-publish.py` captures real npm requests against a loopback
registry. The old archive-path request must fail metadata validation; the directory
request must pass and upload exactly the scanned repacked archive bytes. This test
never contacts or modifies a public registry.
