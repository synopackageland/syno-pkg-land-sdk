// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import SharedFolderSettings from './SharedFolderSettings.vue'
import type {SharedFolderAccess,SharedFolderItem,SharedFolderStatus} from '../context'
import {sharedFolderMessages} from './i18n'

const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await Promise.resolve()}}
const folder=(over:Partial<SharedFolderItem>={}):SharedFolderItem=>({key:'media',name:'Media',path:'/volume1/Media',exists:true,required:'write',granted:'read',state:'pending',...over})
const status=(...folders:SharedFolderItem[]):SharedFolderStatus=>({account:'OpenList',folders,pending:folders.some(f=>f.state==='pending')})
function mount(shared:Partial<SharedFolderAccess>,locale='en'){
 const access={messages:sharedFolderMessages(locale),canGrant:()=>true,grant:async()=>status(),openControlPanel(){},...shared} as SharedFolderAccess
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(SharedFolderSettings,{shared:access,locale}))})
 app.mount(root)
 return {root,unmount:()=>{app.unmount();root.remove()}}
}
const states=(root:HTMLElement)=>Array.from(root.querySelectorAll('[data-shared-folder-state]')).map(n=>n.getAttribute('data-shared-folder-state'))

test('loading shows one placeholder row and never blocks the page',async()=>{
 let answer:(s:SharedFolderStatus)=>void=()=>{}
 const {root,unmount}=mount({status:()=>new Promise(r=>{answer=r})})
 try{
  await settle()
  expect(root.querySelector('[data-placeholder-row]')).not.toBeNull()
  expect(root.querySelector('[aria-busy=true]')).not.toBeNull()
  answer(status(folder({state:'ready',granted:'write'})));await settle()
  expect(root.querySelector('[data-placeholder-row]')).toBeNull()
  expect(states(root)).toEqual(['ready'])
 }finally{unmount()}
})

test('a folder in good standing is shown without any notice or button',async()=>{
 const {root,unmount}=mount({status:async()=>status(folder({state:'ready',granted:'write'}))})
 try{
  await settle()
  expect(root.textContent).toContain('Media')
  expect(root.querySelector('[data-shared-folder-notice]')).toBeNull()
  expect(root.textContent).not.toContain('Grant access')
 }finally{unmount()}
})

test('no configured folder renders nothing, and neither does an unreadable status',async()=>{
 for(const read of [async()=>status(),async()=>{throw {code:'shared_folder_unavailable'}}]){
  const {root,unmount}=mount({status:read})
  try{await settle();expect(root.querySelector('[data-shared-folder],[data-placeholder-row]')).toBeNull()}finally{unmount()}
 }
})

test('a pending folder tells an administrator to grant, with the steps and a Control Panel button',async()=>{
 const open=vi.fn()
 const {root,unmount}=mount({status:async()=>status(folder()),openControlPanel:open})
 try{
  await settle()
  expect(states(root)).toEqual(['pending'])
  const notice=root.querySelector('[data-shared-folder-notice]')!.textContent!
  expect(notice).toContain('System internal user')
  expect(notice).toContain('OpenList')
  expect(notice).toContain('Media')
  const buttons=Array.from(root.querySelectorAll('button'))
  expect(buttons.map(b=>b.textContent!.trim())).toEqual(['Grant access','Open Control Panel'])
  buttons[1]!.click();expect(open).toHaveBeenCalledOnce()
 }finally{unmount()}
})

test('one click grants and the row turns ready from the backend readback',async()=>{
 const grant=vi.fn(async()=>status(folder({state:'ready',granted:'write'})))
 const {root,unmount}=mount({status:async()=>status(folder()),grant})
 try{
  await settle()
  ;Array.from(root.querySelectorAll('button')).find(b=>b.textContent!.trim()==='Grant access')!.click();await settle()
  expect(grant).toHaveBeenCalledWith('media')
  expect(states(root)).toEqual(['ready'])
  expect(root.querySelector('[data-shared-folder-notice]')).toBeNull()
 }finally{unmount()}
})

test('a failed grant says why and keeps the steps',async()=>{
 const {root,unmount}=mount({status:async()=>status(folder()),grant:async()=>{throw {code:'shared_folder_custom_acl'}}})
 try{
  await settle()
  ;Array.from(root.querySelectorAll('button')).find(b=>b.textContent!.trim()==='Grant access')!.click();await settle()
  expect(root.querySelector('[role=alert]')!.textContent).toContain('advanced permissions')
  expect(states(root)).toEqual(['pending'])
 }finally{unmount()}
})

test('without a grant channel the person is asked to involve an administrator and shown the steps',async()=>{
 const {root,unmount}=mount({status:async()=>status(folder()),canGrant:()=>false})
 try{
  await settle()
  expect(root.textContent).not.toContain('Grant access')
  expect(root.querySelector('[data-shared-folder-notice]')!.textContent).toContain('Ask an administrator')
 }finally{unmount()}
})

test('a missing or ambiguous share is a choice problem, not a permission to grant',async()=>{
 const {root,unmount}=mount({status:async()=>status(folder({state:'missing',exists:false,granted:'none'}),folder({key:'b',name:'B',state:'ambiguous',granted:'none'}))})
 try{
  await settle()
  expect(states(root)).toEqual(['missing','ambiguous'])
  expect(Array.from(root.querySelectorAll('button'))).toEqual([])
  expect(root.textContent).toContain('no longer exists')
  expect(root.textContent).toContain('More than one volume')
 }finally{unmount()}
})

test('the text follows the DSM language',async()=>{
 const {root,unmount}=mount({status:async()=>status(folder())},'cht')
 try{await settle();expect(root.textContent).toContain('系統內部使用者');expect(root.textContent).toContain('授予權限')}finally{unmount()}
})
