import {mkdtempSync,rmSync,writeFileSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {fileURLToPath} from 'node:url'
import {build} from 'vite'
import vue from '@vitejs/plugin-vue'
import {expect,test} from 'vitest'
import save from '../i18n/save/de.json'
import delegate from '../i18n/delegate/ko.json'
const external=[/^vue$/,/^@synopackageland\/ui($|\/)/,/^@lucide\/vue($|\/)/]
// An app that imports only the Gate from the published bundle must not carry
// every capability's catalogs.
test('the Gate alone bundles only the authorization catalogs',async()=>{
 const dist=mkdtempSync(join(tmpdir(),'sdk-dist-'))
 try{
  await build({configFile:false,logLevel:'silent',root:fileURLToPath(new URL('..',import.meta.url)),plugins:[vue()],build:{outDir:dist,minify:false,lib:{entry:{index:'src/index.ts',vue:'src/vue.ts'},formats:['es']},rollupOptions:{external}}})
  writeFileSync(join(dist,'gate.js'),"export {AuthorizationGate} from './vue.js'\n")
  const out=await build({configFile:false,logLevel:'silent',build:{write:false,minify:false,lib:{entry:join(dist,'gate.js'),formats:['es'],fileName:'gate'},rollupOptions:{external}}})
  const code=(Array.isArray(out)?out:[out]).flatMap(o=>'output' in o?o.output:[]).map(c=>'code' in c?c.code:'').join('\n')
  expect(code).toContain('Autorisieren')
  expect(code).not.toContain(save.exampleHelp)
  expect(code).not.toContain(delegate.blocked)
 }finally{rmSync(dist,{recursive:true,force:true})}
},120000)
