/** Pure metadata contracts. Native requests and credentials belong to the CLI. */
export type NativeScalar = string | number | boolean | null
export type NativeSchema = Record<string, unknown>
export type NativeBinding = {param: string} | {literal: NativeScalar} | {actionToken: true}
export type NativeProjection = {pointer: string} | {fields: Record<string, string>}
export interface NativeResponse {
 statuses: number[]
 decode: 'json' | 'text' | 'binary'
 maxBytes: number
 contentTypes: string[]
 project?: NativeProjection
}
export type NativeAuth = {
 type: 'header'
 header: 'Authorization'
 prefix: 'Token ' | 'Bearer '
 credentialPage: string
 instructions: string
 verify: {method: 'GET'; path: string; response: Omit<NativeResponse, 'decode'> & {decode: 'json' | 'text'}}
} | {
 type: 'greader-client-login'
 credentialPage: string
 instructions: string
 exchangePath: string
 identityPath: string
 actionTokenPath: string
}
export interface NativeHttp {
 basePath: string
 auth: NativeAuth
 limits: {maxResponseBytes: number; maxTransferBytes: number}
}
export type NativePagination = {
 mode: 'page'; pageParam: string; sizeParam: string
 items: string; next: string; pageSize: number; maxPages: number; maxItems: number; maxBytes: number
} | {
 mode: 'cursor'; cursorParam: string; sizeParam: string
 items: string; next: string; pageSize: number; maxPages: number; maxItems: number; maxBytes: number
}
export type NativeFlow = {type: 'invoke'; method: string}
 | {type: 'paginate-http'; method: string; pagination: NativePagination}
 | {type: 'ndjson'; method: string; maxLines: number; maxLineBytes: number; maxBytes: number}
 | {type: 'upload-http'; method: string; fileParam: string; maxBytes: number; contentTypes: string[];
    encoding: 'multipart'; part: string}
 | {type: 'upload-http'; method: string; fileParam: string; maxBytes: number; contentTypes: string[];
    encoding: 'raw'}
 | {type: 'download-file'; method: string; outputParam: string; maxBytes: number; contentTypes: string[]}
export interface NativeHttpBinding {
 method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
 path: string
 query?: Record<string, NativeBinding>
 headers?: {'Accept'?: string; 'Content-Type'?: string}
 body?: {encoding: 'json' | 'form'; fields: Record<string, NativeBinding>}
 response: NativeResponse
}
export interface NativeInventoryBinding {
 effect: 'read' | 'write' | 'destructive'
 permission: 'authenticated' | 'administrators'
 kind: 'rpc' | 'stream'
 flow: NativeFlow
 http: NativeHttpBinding
}
export interface NativeOperation extends NativeInventoryBinding {
 summary: string
 cli: string[]
 ai: 'read' | 'preview' | 'draft' | 'human-only' | 'hidden'
 preview?: string
 paramsSchema: NativeSchema
 resultSchema: NativeSchema
}
export interface NativeRegistry {
 format: 'synopkgland.api'
 version: 3
 nativeHttp: NativeHttp
 operations: Record<string, NativeOperation>
}
export interface NativePackage {id: string; version: string; displayName: string}
export type NativeEntry = 'policy' | 'admin-only' | 'app-privilege'
export type NativeFeature = 'native-http-v1' | 'auth-header-v1' | 'auth-greader-client-login-v1'
 | 'paginate-http-v1' | 'ndjson-http-v1' | 'upload-http-v1' | 'download-file-v1'
export interface NativeInventory {
 upstreamAuthPaths: string[]
 format: 'synopkgland.http-inventory'
 version: 1
 package: NativePackage
 entry: NativeEntry
 entryPolicyDigest: string
 basePath: string
 registrySha256: string
 authSha256: string
 bindings: Record<string, NativeInventoryBinding>
}
export interface NativeDescriptor {
 format: 'synopkgland.api'
 version: 3
 payload: {
  package: NativePackage
  aliases: string[]
  provenance: {developedBy: 'synopkgland' | 'third-party' | 'unknown'; generator: string}
  entry: NativeEntry
  transport: {path: string; auth: 'dsm-session'}
  execution: NativeHttp & {type: 'native-http'}
  features: NativeFeature[]
  entryPolicyDigest: string
  registrySha256: string
  inventorySha256: string
  authSha256: string
  bindings: Record<string, NativeInventoryBinding>
  commands: Record<string, NativeOperation>
 }
 sha256: string
}
