// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,defineComponent,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import AuthorizationGate from './AuthorizationGate.vue'
import {authorizationCopy} from './i18n'
import {useAuthorization,type AuthorizationControl} from './use-authorization'
import type {AuthorizationAccess,AuthorizationStatus} from '../context'
const cleanup:Array<()=>void>=[]
afterEach(()=>cleanup.splice(0).forEach(fn=>fn()))
async function settle(){for(let i=0;i<20;i++){await Promise.resolve();await nextTick()}}
const reply=(state:AuthorizationStatus['state'],reason?:string):AuthorizationStatus=>({command:'authorization',schema_version:1,state,...(reason?{reason}:{})})
const text=authorizationCopy('en','Reclaim')
const dialog=()=>document.querySelector<HTMLElement>('[role=dialog]')
const button=(label:string)=>Array.from(document.querySelectorAll('button')).find(b=>b.textContent?.trim()===label)
// An App component that runs a root operation reads the Gate through the composable.
function mount(authorization:AuthorizationAccess){
 let control!:AuthorizationControl
 const Operation=defineComponent({setup(){control=useAuthorization();return()=>h('p',{class:'content'},control.state.value)}})
 const root=document.createElement('div');document.body.append(root)
 const ctx={locale:'en',displayName:'Reclaim',authorization,reopen:()=>()=>{}}
 const app=createApp({render:()=>h(AppRoot,{},()=>h(AuthorizationGate,{ctx},()=>h(Operation)))})
 app.config.idPrefix='use-test';app.mount(root);cleanup.push(()=>{app.unmount();root.remove()})
 return {root,control:()=>control}
}
test('the state is checking until the background status arrives',async()=>{
 let resolve!:(s:AuthorizationStatus)=>void
 const {root}=mount({canManage:()=>true,status:()=>new Promise(r=>{resolve=r}),request:vi.fn(),revoke:vi.fn()})
 await settle()
 expect(root.querySelector('.content')?.textContent).toBe('checking')
 resolve(reply('ready'));await settle()
 expect(root.querySelector('.content')?.textContent).toBe('ready')
})
test('requireAuthorization waits for the check and resolves true when ready, without a dialog',async()=>{
 let resolve!:(s:AuthorizationStatus)=>void
 const {control}=mount({canManage:()=>true,status:()=>new Promise(r=>{resolve=r}),request:vi.fn(),revoke:vi.fn()})
 await settle()
 const result=control().requireAuthorization()
 resolve(reply('ready'))
 expect(await result).toBe(true)
 expect(dialog()).toBeNull()
})
test('requireAuthorization opens the dialog again and resolves true once authorized',async()=>{
 const {control}=mount({canManage:()=>true,status:async()=>reply('authorization_required','grant_missing'),request:async()=>reply('ready'),revoke:vi.fn()})
 await settle()
 document.querySelector<HTMLElement>('[role=dialog] button[aria-label]')!.click();await settle()
 expect(dialog()).toBeNull()
 const result=control().requireAuthorization();await settle()
 expect(dialog()?.textContent).toContain(text('title'))
 button(text('authorize'))!.click()
 expect(await result).toBe(true)
})
test('requireAuthorization resolves false when the dialog is dismissed',async()=>{
 const {control}=mount({canManage:()=>true,status:async()=>reply('not_admin'),request:vi.fn(),revoke:vi.fn()})
 await settle()
 const result=control().requireAuthorization();await settle()
 document.querySelector<HTMLElement>('[role=dialog] button[aria-label]')!.click()
 expect(await result).toBe(false)
})
test('the controls refuse to work outside a Gate',()=>{
 const app=createApp(defineComponent({setup(){useAuthorization();return()=>null}}))
 app.config.warnHandler=()=>{}
 expect(()=>app.mount(document.createElement('div'))).toThrow(/AuthorizationGate/)
})
