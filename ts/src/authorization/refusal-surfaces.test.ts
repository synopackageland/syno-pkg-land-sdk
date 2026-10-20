// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import LoginPage from '../identity/LoginPage.vue'
import {createSession} from '../identity/session'
import type {Context} from '../context'
import {delegateCopy,errorCopy} from '../delegate/copy'
import {nasCopy,nasErrorCopy} from '../nas/locale'
import {createNasProvider} from '../nas/provider'
import {saveCopy,saveError} from '../delegate/save-copy'
import {backgroundCopy,backgroundErrorCopy} from '../background/i18n'
import {appRefusal} from './refusal'
import {uiLocales} from '../i18n'

test('all error-copy surfaces use the same remedy, including legacy calls without a locale',()=>{
 for(const locale of uiLocales)for(const code of ['app_privilege_denied','admin_required']){
  const error={code},message=appRefusal(error,locale,'Download Hub')!.message
  expect(errorCopy(error,delegateCopy(locale),locale,'Download Hub')).toBe(message)
  expect(nasErrorCopy(error,nasCopy(locale),locale,'Download Hub')).toBe(message)
  expect(saveError(error,saveCopy(locale),'select',locale,'Download Hub')).toBe(message)
  expect(backgroundErrorCopy(error,backgroundCopy(locale),locale,'Download Hub')).toBe(message)
  expect(errorCopy(error,delegateCopy(locale))).toBe(appRefusal(error,locale)!.message)
  expect(nasErrorCopy(error,nasCopy(locale))).toBe(appRefusal(error,locale)!.message)
 }
 expect(errorCopy({code:'PERMISSION_DENIED'},delegateCopy('en'))).toBe(delegateCopy('en').denied)
 expect(nasErrorCopy({code:'denied'},nasCopy('en'))).toBe(nasCopy('en').errorDenied)
})
test('NAS reads and mutations preserve entry refusals before handlers run',async()=>{
 for(const code of ['app_privilege_denied','admin_required']){
  const provider=createNasProvider({call:vi.fn().mockRejectedValue({code})})
  try{
   await expect(provider.shares(0,new AbortController().signal)).rejects.toEqual({code})
   await expect(provider.createFolder('/home','new',new AbortController().signal)).rejects.toEqual({code})
  }finally{provider.dispose()}
 }
})
test('session discovery and later business refusals expose actionable denial without offering login',async()=>{
 for(const code of ['app_privilege_denied','admin_required']){
  const call=vi.fn().mockRejectedValue({code})
  const session=createSession({call,locale:'zh-TW',displayName:'下載中心'} as unknown as Context)
  const root=document.createElement('div');document.body.append(root)
  const app=createApp({render:()=>h(LoginPage,{session,locale:'zh-TW'})});app.mount(root)
  try{
   await session.start();await nextTick()
   expect(session.enabled.value).toBe(false)
   expect(session.denied.value).toEqual({code,...appRefusal({code},'zh-TW','下載中心')})
   expect(root.querySelector('[role="alert"]')?.textContent).toBe(session.denied.value?.message)
   expect(root.querySelector('form')).toBeNull()
  }finally{app.unmount();session.dispose();root.remove()}
  const later=vi.fn().mockResolvedValueOnce({enabled:false}).mockRejectedValueOnce({code})
  const next=createSession({call:later,locale:'en'} as unknown as Context)
  try{await next.start();await expect(next.call('data')).rejects.toEqual({code});expect(next.denied.value?.code).toBe(code)}finally{next.dispose()}
 }
})

test('a stale business denial cannot replace a newer signed-in identity',async()=>{
 let reject!:(error:unknown)=>void
 const call=vi.fn().mockResolvedValueOnce({enabled:false}).mockImplementationOnce(()=>new Promise((_,r)=>{reject=r})).mockResolvedValueOnce({user:'alice',uid:'1205',expiresAt:new Date(Date.now()+900000).toISOString()})
 const session=createSession({call,locale:'en'} as unknown as Context)
 try{
  await session.start()
  const pending=session.call('data').catch(error=>error)
  await session.login('alice','password','')
  reject({code:'app_privilege_denied'});await pending
  expect(session.subject.value?.user).toBe('alice')
  expect(session.denied.value).toBeUndefined()
 }finally{session.dispose()}
})
test('entry refusal during identity bootstrap or polling clears the subject and keeps the remedy',async()=>{
 vi.useFakeTimers()
 try{
  for(const code of ['app_privilege_denied','admin_required']){
   const call=vi.fn().mockResolvedValueOnce({enabled:true}).mockRejectedValueOnce({code})
   const session=createSession({call,locale:'en'} as unknown as Context)
   try{await session.start();expect(session.denied.value?.code).toBe(code)}finally{session.dispose()}
   const poll=vi.fn().mockResolvedValueOnce({enabled:true}).mockResolvedValueOnce({user:'alice',uid:'1205',expiresAt:new Date(Date.now()+900000).toISOString()}).mockRejectedValueOnce({code})
   const next=createSession({call:poll,locale:'en'} as unknown as Context)
   try{await next.start();await vi.advanceTimersByTimeAsync(60000);expect(next.subject.value).toBeUndefined();expect(next.denied.value?.code).toBe(code)}finally{next.dispose()}
  }
 }finally{vi.useRealTimers()}
})
