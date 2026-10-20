// @vitest-environment jsdom
// @vitest-environment-options {"url":"https://nas.test"}
import {expect, test, vi} from 'vitest'
import {createApp, h, nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import LoginPage from './LoginPage.vue'
import {createSession} from './session'
import type {Context} from '../context'
async function settle(){for(let i=0;i<20;i++){await Promise.resolve();await nextTick()}}
function fill(root: HTMLElement, selector: string, value: string){const field=root.querySelector<HTMLInputElement>(selector)!;field.value=value;field.dispatchEvent(new Event('input',{bubbles:true}))}
function button(root: HTMLElement, text: string){return Array.from(root.querySelectorAll<HTMLButtonElement>('button')).find(b=>b.textContent?.trim()===text)!}
test('OTP focuses the enabled code field and a successful login signs the session in',async()=>{
 const call=vi.fn(async(method: string, params?: Record<string, unknown>)=>{
  if(method==='identity.status')return {enabled:true}
  if(method==='identity.session')throw {code:'session_required'}
  if(method==='identity.login') {if(!params?.otp)throw {code:'otp_required'};return {user:'alice',uid:'1205',expiresAt:new Date(Date.now()+900000).toISOString()}}
  return {}
 })
 const session=createSession({locale:'en',call,reopen(){return ()=>{}}} as unknown as Context)
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(LoginPage,{session,locale:'en'}))});app.config.idPrefix='login-test';app.mount(root)
 try{
  await session.start();await settle()
  fill(root,'input[autocomplete="username"]','alice');fill(root,'input[type="password"]','password');await settle();button(root,'Sign in').click();await settle()
  const otp=root.querySelector<HTMLInputElement>('input[autocomplete="one-time-code"]')!
  expect(otp.disabled).toBe(false);expect(document.activeElement).toBe(otp)
  fill(root,'input[autocomplete="one-time-code"]','123456');await settle();button(root,'Verify and sign in').click();await settle()
  expect(session.subject.value?.user).toBe('alice')
  expect(call).toHaveBeenLastCalledWith('identity.login',{account:'alice',password:'password',otp:'123456'})
 }finally{app.unmount();session.dispose();root.remove()}
})
test('a sign-in the origin guard refuses explains that the page must be opened again',async()=>{
 for(const code of ['cross_origin','origin_required']){
  const call=vi.fn(async(method: string)=>{
   if(method==='identity.status')return {enabled:true}
   if(method==='identity.session')throw {code:'session_required'}
   if(method==='identity.login')throw {code}
   return {}
  })
  const session=createSession({locale:'en',call,reopen(){return ()=>{}}} as unknown as Context)
  const root=document.createElement('div');document.body.append(root)
  const app=createApp({render:()=>h(AppRoot,{},()=>h(LoginPage,{session,locale:'en'}))});app.config.idPrefix='login-origin';app.mount(root)
  try{
   await session.start();await settle()
   fill(root,'input[autocomplete="username"]','alice');fill(root,'input[type="password"]','password');await settle();button(root,'Sign in').click();await settle()
   expect(root.querySelector('[role="alert"]')?.textContent,code).toBe("Sign-in was refused: the browser did not show that it came from this App's page. Open the App again in a current browser.")
  }finally{app.unmount();session.dispose();root.remove()}
 }
})
