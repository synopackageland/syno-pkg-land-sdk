// Compile-only checks for client contract discrimination; no runtime or credentials.
import type {NativeAuth, NativeBinding, NativeFlow, NativeDescriptor, NativeInventory} from '../src/native-http'
function authPath(auth: NativeAuth): string {
 if (auth.type === 'header') return auth.verify.path
 return auth.exchangePath
}
function transfer(flow: NativeFlow): string | undefined {
 if (flow.type === 'upload-http' && flow.encoding === 'multipart') return flow.part
 if (flow.type === 'download-file') return flow.outputParam
 return undefined
}
const action: NativeBinding = {actionToken: true}
// @ts-expect-error Arbitrary auth recipes are outside the contract.
const recipe: NativeAuth = {type: 'script', command: 'fixture'}
// @ts-expect-error Multipart uploads must declare a part.
const multipart: NativeFlow = {type:'upload-http', method:'files.upload', fileParam:'file', encoding:'multipart', maxBytes:1024, contentTypes:['application/pdf']}
// @ts-expect-error Raw uploads cannot declare a multipart part.
const raw: NativeFlow = {type:'upload-http', method:'files.upload', fileParam:'file', encoding:'raw', part:'file', maxBytes:1024, contentTypes:['application/xml']}
function bindingHashes(descriptor: NativeDescriptor, inventory: NativeInventory): string[] {
 return [descriptor.sha256, descriptor.payload.authSha256, inventory.registrySha256, ...inventory.upstreamAuthPaths]
}
void [authPath, transfer, action, recipe, multipart, raw, bindingHashes]
