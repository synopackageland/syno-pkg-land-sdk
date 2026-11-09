// @vitest-environment jsdom
// @vitest-environment-options {"url":"http://nas.test:5000/?launchApp=Example&SynoToken=private#secret"}
import {afterEach, expect, test, vi} from 'vitest'
import {createApp, h, nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import LoginPage from './LoginPage.vue'
import {createSession} from './session'
import {identityCopy} from './i18n'
import type {Context} from '../context'

async function settle() {for(let i=0;i<10;i++) {await Promise.resolve(); await nextTick()}}
afterEach(() => vi.unstubAllGlobals())
function mount(user?: Context['user'], webApi = vi.fn(async () => ({port_info:[{port_id:'dms_https',protocol:'tcp',dst_port:['7443']}]}))) {
 const call = vi.fn()
 const session = createSession({locale:'en',call,user,webApi} as unknown as Context)
 session.enabled.value = true // as after identity.status answered {enabled:true}
 const discovery=vi.spyOn(session,'readHttpsURL')
 const root = document.createElement('div'); document.body.append(root)
 const app = createApp({render:() => h(AppRoot, {colorScheme:'dark',locale:'en'}, () => h(LoginPage, {session,locale:'en'}))})
 app.config.idPrefix='login-http'; app.mount(root)
 return {root,session,call,webApi,discovery,close() {app.unmount(); session.dispose(); root.remove()}}
}

test('HTTP explains HTTPS, offers the discovered link, and keeps both sign-in paths disabled after dismissal', async () => {
 const fixture = mount({name:'operator',isAdmin:true})
 const {root,call,webApi} = fixture
 try {
  await settle()
  const copy=identityCopy('en')
  expect(root.textContent).toContain(copy.httpsReason)
  expect(root.textContent).toContain(copy.httpsInstructions)
  expect(webApi).toHaveBeenCalledExactlyOnceWith('SYNO.Core.Service.PortInfo','load',1,{})
  const open=Array.from(root.querySelectorAll<HTMLButtonElement>('button')).find(button=>button.textContent===copy.openHTTPS)!
  expect(open.disabled).toBe(false)
  const assign=vi.fn()
  const actualWindow=window
  vi.stubGlobal('window',new Proxy(actualWindow,{get(target,key) {return key==='location'?{assign}:Reflect.get(target,key,target)}}))
  open.click(); await settle(); vi.unstubAllGlobals()
  expect(assign).toHaveBeenCalledExactlyOnceWith('https://nas.test:7443/webman/?launchApp=Example')
  root.querySelector<HTMLButtonElement>('button[aria-label="Close"]')!.click(); await settle()
  expect(root.querySelector<HTMLElement>('.ui-callout[data-tone="warning"]')?.hidden).toBe(true)
  for(const label of [copy.signIn,copy.continueDSM]) {
   const button=Array.from(root.querySelectorAll<HTMLButtonElement>('button')).find(button=>button.textContent===label)!
   expect(button.disabled).toBe(true); button.click()
  }
  root.querySelector('form')!.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}))
  await settle()
  expect(call).not.toHaveBeenCalled()
  expect(Array.from(root.querySelectorAll<HTMLInputElement>('input')).every(input=>input.disabled)).toBe(true)
 } finally {fixture.close()}
})

test.each([undefined,{name:'viewer',isAdmin:false}])('unknown/ordinary users see instructions without PortInfo or a guessed HTTPS link: %s', async user => {
 const fixture=mount(user)
 try {
  await settle()
  expect(fixture.webApi).not.toHaveBeenCalled()
  expect(fixture.root.textContent).toContain(identityCopy('en').httpsInstructions)
  expect(fixture.root.textContent).not.toContain(identityCopy('en').openHTTPS)
 } finally {fixture.close()}
})

test.each(['missing','ambiguous','refused'])('unavailable HTTPS discovery retains instructions: %s', async failure => {
 const webApi=vi.fn(async()=>{
  if(failure==='refused') throw new Error('refused')
  return {port_info: failure==='missing'?[]:[{port_id:'dms_https',protocol:'tcp',dst_port:['7443','8443']}]}
 })
 const fixture=mount({name:'operator',isAdmin:true},webApi)
 try {
  await settle()
  expect(fixture.root.textContent).toContain(identityCopy('en').httpsInstructions)
  expect(fixture.root.textContent).not.toContain(identityCopy('en').openHTTPS)
 } finally {fixture.close()}
})

test('unmount cancels pending HTTPS discovery', async () => {
 let resolve!:(value:{port_info:[]})=>void
 const webApi=vi.fn(()=>new Promise<{port_info:[]}>(done=>{resolve=done}))
 const fixture=mount({name:'operator',isAdmin:true},webApi)
 const signal=fixture.discovery.mock.calls[0][1]!
 expect(signal.aborted).toBe(false)
 fixture.close()
 expect(signal.aborted).toBe(true)
 resolve({port_info:[]}); await settle()
 expect(fixture.root.childElementCount).toBe(0)
})


test('a late HTTPS address does not reopen a dismissed warning',async()=>{
 let resolve!:(value:{port_info:{port_id:string;protocol:string;dst_port:string[]}[]})=>void
 const webApi=vi.fn(()=>new Promise<{port_info:{port_id:string;protocol:string;dst_port:string[]}[]}>(done=>{resolve=done}))
 const fixture=mount({name:'operator',isAdmin:true},webApi)
 try {
  await settle()
  fixture.root.querySelector<HTMLButtonElement>('button[aria-label="Close"]')!.click();await settle()
  resolve({port_info:[{port_id:'dms_https',protocol:'tcp',dst_port:['7443']}]});await settle()
  expect(fixture.root.querySelector<HTMLElement>('.ui-callout[data-tone="warning"]')?.hidden).toBe(true)
 } finally {fixture.close()}
})
