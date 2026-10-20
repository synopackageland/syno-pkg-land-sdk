import {existsSync,readFileSync} from 'node:fs'
import {dirname,resolve} from 'node:path'
import {expect,test} from 'vitest'
import * as headless from '../src/index'
import * as vue from '../src/vue'

const src=resolve(__dirname,'../src')
// Every module the headless entry reaches through static imports, type-only ones included.
function reachable(file:string,seen=new Set<string>()):Set<string>{
 if(seen.has(file))return seen
 seen.add(file)
 for(const [,spec] of [...readFileSync(file,'utf8').matchAll(/(?:import|export)[^'"]*?from\s*'([^']+)'|import\('([^']+)'\)/g)].map(m=>[m[0],m[1]??m[2]])){
  if(!spec!.startsWith('.')){seen.add(spec!);continue}
  if(spec!.endsWith('.json')||spec!.endsWith('.vue')){seen.add(resolve(dirname(file),spec!));continue}
  const target=resolve(dirname(file),spec!)
  reachable(existsSync(target)?target:target.replace(/\.js$/,'')+'.ts',seen)
 }
 return seen
}

test('the headless entry never reaches vue, the UI or a component',()=>{
 const modules=[...reachable(resolve(src,'index.ts'))]
 expect(modules.filter(m=>m==='vue'||m.startsWith('@synopackageland/ui')||m.startsWith('@lucide/')||m.endsWith('.vue'))).toEqual([])
})

test('the native entry never reaches vue, the UI or a component',()=>{
 const modules=[...reachable(resolve(src,'native.ts'))]
 expect(modules.filter(m=>m==='vue'||m.startsWith('@synopackageland/ui')||m.startsWith('@lucide/')||m.endsWith('.vue'))).toEqual([])
})

test('the package exports the native entry the build produces',()=>{
 const pkg=JSON.parse(readFileSync(resolve(__dirname,'../package.json'),'utf8'))
 expect(pkg.exports['./native']).toEqual({types:'./dist/src/native.d.ts',import:'./dist/native.js'})
 expect(readFileSync(resolve(__dirname,'../vite.config.ts'),'utf8')).toContain("native: 'src/native.ts'")
})

test('the Vue entry exports the components and helpers templates use',()=>{
 for(const name of ['SharedFolderPermissionDialog','AppShell','BackgroundPanel','FilesPage','LoginPage','PortalSettings','SharedFolderSettings','ExecutorSettings','DelegatePanel','BackgroundSavePanel','BackgroundSaveSettings','AuthorizationGate','AuthorizationSettings','useAuthorization','NasPicker','FileBrowser','createSession','shellPages','uiLocale','identityCopy','ExecutorSetupKey','useExecutorSetup','createBackgroundSaveFacade','createBackgroundPickerProvider','saveCopy'])
  expect(vue,name).toHaveProperty(name)
})

test('the Vue entry re-exports the headless entry',()=>{
 for(const [name,value] of Object.entries(headless))expect(vue[name as keyof typeof vue],name).toBe(value)
 for(const name of ['createSharedFolderPermission','sharedFolderPermissionMessage','shellPages','uiLocale','identityCopy','saveCopy','saveError','createBackgroundSaveFacade','createBackgroundPickerProvider','createNasProvider','scopedPicker','requestKey','createPortalAccess','portalMessages','portalCatalogs','createSharedFolderAccess','sharedFolderAccessFor','sharedFolderMessages','sharedFolderCatalogs'])
  expect(headless,name).toHaveProperty(name)
})
