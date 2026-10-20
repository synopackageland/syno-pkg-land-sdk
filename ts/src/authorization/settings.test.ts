// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import AuthorizationGate from './AuthorizationGate.vue'
import AuthorizationSettings from './AuthorizationSettings.vue'
import {authorizationCopy} from './i18n'
import type {AuthorizationAccess,AuthorizationStatus} from '../context'
const cleanup:Array<()=>void>=[]
afterEach(()=>cleanup.splice(0).forEach(fn=>fn()))
async function settle(){for(let i=0;i<20;i++){await Promise.resolve();await nextTick()}}
const reply=(state:AuthorizationStatus['state'],reason?:string):AuthorizationStatus=>({command:'authorization',schema_version:1,state,...(reason?{reason}:{})})
const text=authorizationCopy('en','Reclaim')
const dialog=()=>document.querySelector<HTMLElement>('[role=dialog]')
const button=(label:string)=>Array.from(document.querySelectorAll('button')).find(b=>b.textContent?.trim()===label)
// The App's settings page places the section anywhere inside the Gate.
function mount(authorization:AuthorizationAccess){
 const root=document.createElement('div');document.body.append(root)
 const ctx={locale:'en',displayName:'Reclaim',authorization,reopen:()=>()=>{}}
 const app=createApp({render:()=>h(AppRoot,{},()=>h(AuthorizationGate,{ctx},()=>h('main',{class:'settings'},[h(AuthorizationSettings)])))})
 app.config.idPrefix='settings-test';app.mount(root);cleanup.push(()=>{app.unmount();root.remove()})
 return root
}
test('the section shows the authorization status and what it is for',async()=>{
 const root=mount({canManage:()=>false,status:async()=>reply('ready'),request:vi.fn(),revoke:vi.fn()})
 await settle()
 const section=root.querySelector('.settings')!.textContent
 expect(section).toContain(text('settings.title'))
 expect(section).toContain(text('settings.status'))
 expect(section).toContain(text('ready'))
 expect(section).toContain(text('purpose'))
 expect(button(text('revoke'))).toBeUndefined()
})
test('a manager revokes after confirming, and the Gate asks again',async()=>{
 const revoke=vi.fn(async()=>reply('authorization_required','grant_missing'))
 const root=mount({canManage:()=>true,status:async()=>reply('ready'),request:vi.fn(),revoke})
 await settle()
 button(text('revoke'))!.click();await settle()
 expect(dialog()?.textContent).toContain(text('revoke.title'))
 expect(revoke).not.toHaveBeenCalled()
 Array.from(dialog()!.querySelectorAll('button')).find(b=>b.textContent?.trim()===text('revoke'))!.click();await settle()
 expect(revoke).toHaveBeenCalledOnce()
 expect(dialog()?.textContent).toContain(text('title'))
 expect(root.querySelector('.settings')!.textContent).toContain(text('state.required'))
 expect(button(text('revoke'))).toBeUndefined()
})
test('a status that is not ready offers the details again',async()=>{
 const root=mount({canManage:()=>true,status:async()=>reply('authorization_required','grant_missing'),request:vi.fn(),revoke:vi.fn()})
 await settle()
 document.querySelector<HTMLElement>('[role=dialog] button[aria-label]')!.click();await settle()
 expect(dialog()).toBeNull()
 expect(root.querySelector('.settings')!.textContent).toContain(text('state.required'))
 button(text('settings.details'))!.click();await settle()
 expect(dialog()?.textContent).toContain(text('title'))
})
