import {readFileSync, writeFileSync} from 'node:fs'
import {createHash} from 'node:crypto'
import {fileURLToPath} from 'node:url'
import {dirname, join} from 'node:path'
const root = dirname(fileURLToPath(import.meta.url))
const read = p => JSON.parse(readFileSync(join(root,p),'utf8'))
const write = (p,v) => writeFileSync(join(root,p), JSON.stringify(v,null,2)+'\n')
function jcs(v) {
 if(v===null || typeof v!=='object') return JSON.stringify(v)
 if(Array.isArray(v)) return '['+v.map(jcs).join(',')+']'
 return '{'+Object.keys(v).sort().map(k=>JSON.stringify(k)+':'+jcs(v[k])).join(',')+'}'
}
const sha = text => createHash('sha256').update(text).digest('hex')
const digest = v => sha(jcs(v))
const c=read('build-config.json'), r=read('valid/header-page-files.json')
// Existing gateway policy serialization has fixed struct order and normalized empties.
const g=c.gateway
const policy={entry:g.entry??'',access:g.access??'',upstream_access:g.upstream_access??'',groups:g.groups??[],compose_blocked:g.compose_blocked??false,upstream_auth_paths:g.upstream_auth_paths??[],passthrough:g.passthrough??[],optional_passthrough:g.optional_passthrough??{}}
const entryPolicyDigest=sha(JSON.stringify(policy))
const registrySha256=digest(r)
const authSha256=digest({basePath:r.nativeHttp.basePath,auth:r.nativeHttp.auth,entryPolicyDigest})
const bindings=Object.fromEntries(Object.entries(r.operations).map(([name,o])=>[name,{effect:o.effect,permission:o.permission,kind:o.kind,flow:o.flow,http:o.http}]))
const inventory={upstreamAuthPaths:g.upstream_auth_paths,format:'synopkgland.http-inventory',version:1,package:c.package,entry:g.entry??'policy',entryPolicyDigest,basePath:r.nativeHttp.basePath,registrySha256,authSha256,bindings}
const descriptor={format:'synopkgland.api',version:3,payload:{package:c.package,aliases:c.aliases,provenance:c.provenance,entry:inventory.entry,transport:{path:`/webman/3rdparty/${c.package.id}/cgi/api.cgi`,auth:'dsm-session'},execution:{type:'native-http',...r.nativeHttp},features:['native-http-v1','auth-header-v1','auth-greader-client-login-v1','paginate-http-v1','ndjson-http-v1','upload-http-v1','download-file-v1'],entryPolicyDigest,registrySha256,inventorySha256:digest(inventory),authSha256,bindings,commands:r.operations}}
descriptor.sha256=digest(descriptor)
write('descriptor.json',descriptor)
write('inventory.json',inventory)
write('artifact-digests.json',{entryPolicyJSON:JSON.stringify(policy),entryPolicyDigest,registrySha256,authSha256,inventorySha256:digest(inventory),descriptionSha256:descriptor.sha256})
