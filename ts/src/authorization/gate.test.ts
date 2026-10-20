// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import AuthorizationGate from './AuthorizationGate.vue'
import {authorizationCopy} from './i18n'
import type {AuthorizationAccess,AuthorizationStatus} from '../context'
const cleanup:Array<()=>void>=[]
afterEach(()=>{cleanup.splice(0).forEach(fn=>fn());vi.useRealTimers()})
async function settle(){for(let i=0;i<20;i++){await Promise.resolve();await nextTick()}}
const reply=(state:AuthorizationStatus['state'],reason?:string):AuthorizationStatus=>({command:'authorization',schema_version:1,state,...(reason?{reason}:{})})
function mount(authorization:AuthorizationAccess){
 const root=document.createElement('div');document.body.append(root)
 // Only what the Gate reads: an App that uses only the Gate has no call or host to give.
 const ctx={locale:'en',displayName:'Reclaim',authorization,reopen:()=>()=>{}}
 const app=createApp({render:()=>h(AppRoot,{},()=>h(AuthorizationGate,{ctx},()=>h('p',{class:'content'},'app content')))})
 app.config.idPrefix='gate-test';app.mount(root);cleanup.push(()=>{app.unmount();root.remove()})
 return root
}
const text=authorizationCopy('en','Reclaim')
const dialog=()=>document.querySelector<HTMLElement>('[role=dialog]')
const button=(label:string)=>Array.from(document.querySelectorAll('button')).find(b=>b.textContent?.trim()===label)
test('content renders at once while the status is checked in the background',async()=>{
 const root=mount({canManage:()=>true,status:()=>new Promise<AuthorizationStatus>(()=>{}),request:vi.fn(),revoke:vi.fn()})
 await settle()
 expect(root.querySelector('.content')?.textContent).toBe('app content')
 expect(document.body.textContent).not.toContain(text('checking'))
 expect(dialog()).toBeNull()
})
test('a ready authorization adds nothing around the content',async()=>{
 const root=mount({canManage:()=>true,status:async()=>reply('ready'),request:vi.fn(),revoke:vi.fn()})
 await settle()
 expect(root.querySelector('.content')).not.toBeNull()
 expect(document.body.textContent).not.toContain(text('ready'))
 expect(button(text('revoke'))).toBeUndefined()
 expect(dialog()).toBeNull()
})
test('a required authorization opens a dialog whose Authorize approves and closes it',async()=>{
 const request=vi.fn(async()=>reply('ready'))
 const root=mount({canManage:()=>true,status:async()=>reply('authorization_required','grant_missing'),request,revoke:vi.fn()})
 await settle()
 expect(root.querySelector('.content')).not.toBeNull()
 expect(dialog()?.textContent).toContain(text('title'))
 button(text('authorize'))!.click();await settle()
 expect(request).toHaveBeenCalledOnce()
 expect(dialog()).toBeNull()
 expect(root.querySelector('.content')).not.toBeNull()
})
test('a non-administrator gets a dialog without Authorize',async()=>{
 mount({canManage:()=>false,status:async()=>reply('not_admin'),request:vi.fn(),revoke:vi.fn()})
 await settle()
 expect(dialog()?.textContent).toContain(text('not_admin.title'))
 expect(button(text('authorize'))).toBeUndefined()
})
// O1: under load the first status may be unavailable; the Gate retries before it asks.
test('an unavailable background check retries twice before it opens the dialog',async()=>{
 vi.useFakeTimers({toFake:['setTimeout']})
 const status=vi.fn(async()=>({state:'ready'}) as unknown as AuthorizationStatus)
 mount({canManage:()=>false,status,request:vi.fn(),revoke:vi.fn()})
 await settle()
 expect(status).toHaveBeenCalledTimes(1)
 expect(dialog()).toBeNull()
 await vi.advanceTimersByTimeAsync(1000);await settle()
 expect(status).toHaveBeenCalledTimes(2)
 expect(dialog()).toBeNull()
 await vi.advanceTimersByTimeAsync(3000);await settle()
 expect(status).toHaveBeenCalledTimes(3)
 expect(dialog()?.textContent).toContain(text('unavailable.title'))
 await vi.advanceTimersByTimeAsync(60000);await settle()
 expect(status).toHaveBeenCalledTimes(3)
})
test('a retry that finds the authorization ready never opens the dialog',async()=>{
 vi.useFakeTimers({toFake:['setTimeout']})
 const status=vi.fn().mockRejectedValueOnce(Error('busy')).mockResolvedValue(reply('ready'))
 const root=mount({canManage:()=>false,status,request:vi.fn(),revoke:vi.fn()})
 await settle();await vi.advanceTimersByTimeAsync(1000);await settle()
 expect(status).toHaveBeenCalledTimes(2)
 expect(dialog()).toBeNull()
 expect(root.querySelector('.content')).not.toBeNull()
})
test('an expired session is not retried and offers no Authorize',async()=>{
 vi.useFakeTimers({toFake:['setTimeout']})
 const status=vi.fn().mockRejectedValue({code:'session_required'})
 mount({canManage:()=>true,status,request:vi.fn(),revoke:vi.fn()})
 await settle();await vi.advanceTimersByTimeAsync(5000);await settle()
 expect(status).toHaveBeenCalledOnce()
 expect(dialog()?.textContent).toContain(text('session_expired.title'))
 expect(button(text('authorize'))).toBeUndefined()
})
// a page without DSM's SynoToken (a restored session, an Application
// Portal page) is not temporary: the Gate says to sign in again, once.
test('a check refused for a missing sign-in token says to sign in again and is not retried',async()=>{
 vi.useFakeTimers({toFake:['setTimeout']})
 const status=vi.fn().mockRejectedValue({code:'credential_not_ready'})
 mount({canManage:()=>true,status,request:vi.fn(),revoke:vi.fn()})
 await settle();await vi.advanceTimersByTimeAsync(5000);await settle()
 expect(status).toHaveBeenCalledOnce()
 expect(dialog()?.textContent).toContain(text('credential.title'))
 expect(dialog()?.textContent).toContain(text('credential.body'))
 expect(dialog()?.textContent).not.toContain(text('failed'))
 expect(button(text('authorize'))).toBeUndefined()
})
