# API metadata v3: native HTTP

The authoritative machine-readable contract is
[`api/nativehttp/schema`](../../../api/nativehttp/schema); the runtime embeds the
same files. See the [contract](../../../api/nativehttp/README.md) for semantic
validation, hashes, limits, admission and client execution responsibilities.

Use the [shared conformance corpus](../../../api/nativehttp/testdata/cases.json)
and [digest vectors](../../../api/nativehttp/testdata/canonical-vectors.json).
The [independent client artifact vectors](../../../api/nativehttp/testdata/artifact-digests.json)
include the existing gateway policy serialization and all v3 artifact hashes.

Registry/descriptor v3 is opt-in with `ctl.transport: native-http`. Existing v1/v2
RPC documents and validators retain their original behavior. A client lacking
the exact declared capabilities must refuse v3; it must never fall back to RPC.
The CGI remains a metadata-only DSM authenticated entry and is not the native API
URL. Native credentials, HTTP execution and file transfer remain on the client.

Build tools call `nativehttp.Export(registry, resolvedConfig)` or run:

```sh
go run ./api/nativehttp/cmd/export \
  -registry api/nativehttp/testdata/valid/header-page-files.json \
  -config api/nativehttp/testdata/build-config.json
```

The exporter writes `{descriptor,inventory}` JSON to stdout and does not start an
App. Package `descriptor` as private `synopkgland-api.json` and `inventory` as
private `synopkgland-http-inventory.json`; neither belongs under anonymous UI
assets. `Artifacts.Encode()` returns the two JSON files independently.

CLI packaging glue uses the existing DSM authenticator and entry gate with
`nativehttp.NewDescriptionHandler(auth, descriptorPath, inventoryPath)` in the
existing CGI entry. No business handlers or second resident process are needed.
Build/inspect additionally compare package/version to final INFO, resolved
transport/ingress to the registry and policy digest to the actual gateway config.
Metadata equality is not proof of upstream token validity or native permissions.
