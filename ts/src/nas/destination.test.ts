// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,h,nextTick,ref} from 'vue'
import {AppRoot,ConfigProvider} from '@synopackageland/ui'
import NasPicker from './NasPicker.vue'
import {createNasProvider} from './provider'
import {nasCopy} from './locale'
const cleanup:Array<()=>void>=[]
afterEach(()=>cleanup.splice(0).forEach(fn=>fn()))
async function settle(){for(let i=0;i<30;i++){await Promise.resolve();await nextTick()}}
const grant={id:'d1',key:'save',path:'/home',username:'fixtureuser1',lifetime:'until-revoked' as const}
function fixture(selectDestination:any,mode:'folder'|'file'='folder',extra:Record<string,unknown>={}){
 const root=document.createElement('div');document.body.append(root)
 const selected=vi.fn(),authorized=vi.fn(),refused=vi.fn(),open=ref(true)
 const provider=createNasProvider({call:vi.fn(async()=>({shares:[{name:'home',path:'/home',isdir:true}],offset:0,total:1})) as any})
 const app=createApp({render:()=>h(ConfigProvider,{locale:'en'},()=>h(AppRoot,{},()=>h(NasPicker,{provider,locale:'en',mode,open:open.value,destination:{key:'save',selectDestination},onSelected:selected,onAuthorized:authorized,onRefused:refused,'onUpdate:open':v=>open.value=v,...extra})))})
 app.config.idPrefix='destination-test';app.mount(root)
 cleanup.push(()=>{app.unmount();provider.dispose();root.remove()})
 return {root,open,selected,authorized,refused,app}
}
function button(root:HTMLElement,label:string){const b=Array.from(root.querySelectorAll('button')).find(n=>n.textContent?.trim()===label||n.getAttribute('aria-label')===label);expect(b,label).toBeTruthy();return b!}
async function select(root:HTMLElement){await settle();root.querySelector('tr[data-row-key="/home"]')!.dispatchEvent(new MouseEvent('click',{bubbles:true}));await settle();button(root,nasCopy('en').confirm).click();await settle()}
test('destination confirmation authorizes once with its manifest key, without a second consent dialog',async()=>{
 const call=vi.fn(async()=>grant),f=fixture(call);await settle()
 expect(f.root.textContent).toContain('after you sign out')
 await select(f.root)
 expect(call).toHaveBeenCalledExactlyOnceWith({key:'save',path:'/home'})
 expect(f.authorized).toHaveBeenCalledExactlyOnceWith(grant)
 expect(f.selected).not.toHaveBeenCalled();expect(f.open.value).toBe(false)
})
test('a rejected write/session check keeps the picker open and never emits a path or a grant',async()=>{
 const f=fixture(vi.fn(async()=>{throw {code:'PERMISSION_DENIED',message:'secret SID'}}));await select(f.root)
 expect(f.open.value).toBe(true);expect(f.selected).not.toHaveBeenCalled();expect(f.authorized).not.toHaveBeenCalled()
 expect(f.root.querySelector('[role=alert]')?.textContent).toContain('permission')
 expect(f.root.textContent).not.toContain('secret SID')
})
test('only folder destinations can authorize; a malformed grant is never reported as successful',async()=>{
 const call=vi.fn(async()=>({...grant,path:'/elsewhere'})),f=fixture(call);await select(f.root)
 expect(f.open.value).toBe(true);expect(f.authorized).not.toHaveBeenCalled()
 const fileCall=vi.fn(async()=>grant),file=fixture(fileCall,'file');await settle()
 expect(button(file.root,nasCopy('en').confirm).disabled).toBe(true);expect(fileCall).not.toHaveBeenCalled()
})
test('pending authorization prevents duplicate confirmation and ignores a late result after unmount',async()=>{
 let resolve!:(value:typeof grant)=>void
 const call=vi.fn(()=>new Promise(r=>resolve=r)),f=fixture(call);await select(f.root)
 expect(button(f.root,nasCopy('en').confirm).disabled).toBe(true)
 f.app.unmount();resolve(grant);await settle();expect(f.authorized).not.toHaveBeenCalled()
})
// Application Privileges refuse the address, not the folder, so the
// picker closes and hands the refusal to its owner; no grant exists.
test('an Application Privileges refusal closes the picker and reports the refusal',async()=>{
 const f=fixture(vi.fn(async()=>{throw {code:'APP_DENIED'}}));await select(f.root)
 expect(f.open.value).toBe(false);expect(f.authorized).not.toHaveBeenCalled()
 expect(f.refused).toHaveBeenCalledExactlyOnceWith({code:'APP_DENIED'})
})
test("a refused folder choice uses the folder-choice copy, or the App's own",async()=>{
 const f=fixture(vi.fn(async()=>{throw {code:'PERMISSION_DENIED'}}));await select(f.root)
 expect(f.root.querySelector('[role=alert]')?.textContent).not.toContain('do not need to choose')
 const custom=fixture(vi.fn(async()=>{throw {code:'PERMISSION_DENIED'}}),'folder',{errorText:(e:{code?:string})=>e.code==='PERMISSION_DENIED'?'Ask Gopeed support':undefined})
 await select(custom.root)
 expect(custom.root.querySelector('[role=alert]')?.textContent).toBe('Ask Gopeed support')
})
