# App backend delegate DTOs v1

`dtos.schema.json` is the single source for the wire DTOs shared by Go's
`delegate` package and the TypeScript background-save facade. It covers
destination selection, grants, capability status, operation references,
receipts, operation results and App-owned move mirrors. These are App backend
DTOs; executor request parameters remain in `spec/protocol/v1`.

Run `go generate ./delegate` at the repository root after editing the schema.
It generates `delegate/dtos_gen.go` and `ts/src/delegate/dtos-gen.ts` together.
`go test ./internal/delegategen` rejects either artifact if it differs from the
spec, including field names, types, optional fields, nullability and TS enums.
`ts/test/delegate-dto-types.test.ts` keeps public exports and App/UI projections
bound to those wire types during `npm run typecheck`.

JSON Schema `required` controls field presence; `anyOf` with `null` controls
nullability. An optional pointer with `omitempty` does not emit JSON `null`.
`x-go-pointer` preserves pointer storage for optional Go fields, while
`x-go-type` preserves `uint64`, `int64` and `json.Number` only on plain `integer`
schemas. Generation and `--check` reject incompatible overrides before writing
either artifact, including overrides on `$ref` or `anyOf` wrappers. For a
nullable integer, place the override on the integer branch of `anyOf`.
`x-go-name` maps a
schema type to its existing public Go name, and `x-go-order` preserves Go
struct layout and JSON output order. Go keeps existing string fields for
enums and constants for source compatibility; TS exposes their literal types.

`BackgroundSaveStatus` uses UI names such as `scopeMode`; `SaveOperation` joins
live operation evidence with App metadata; `AppSaveJob` accepts only the
metadata projection of `MoveMirror`. These are views, rather than additional
wire contracts. Runtime validation and the existing ownership checks still
apply; generated types do not validate untrusted JSON.
