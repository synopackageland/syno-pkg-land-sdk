// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import SharedFolderPermissionDialog from './SharedFolderPermissionDialog.vue'
import {createSharedFolderPermission,type SharedFolderPermission} from './permission'

const settle=async()=>{for(let i=0;i<15;i++){await nextTick();await Promise.resolve()}}
const issue={account:'ExampleApp',share:'Media',required:'write'}
const cleanups:Array<()=>void>=[]
afterEach(()=>{cleanups.splice(0).forEach(stop=>stop())})
function mount(permission:SharedFolderPermission){
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>[h('p','App content'),h(SharedFolderPermissionDialog,{permission})])})
 app.mount(root)
 const stop=()=>{app.unmount();root.remove()};cleanups.push(stop)
 return root
}
test('loading and success keep the App visible with no dialog and no repeat check',async()=>{
 let answer:(value:unknown)=>void=()=>{}
 const call=vi.fn(()=>new Promise(r=>{answer=r}))
 const root=mount(createSharedFolderPermission({call:call as never,locale:'en'}))
 expect(root.textContent).toContain('App content');expect(document.querySelector('[role=dialog]')).toBeNull()
 answer({account:'ExampleApp',folders:[]});await settle()
 expect(document.querySelector('[role=dialog]')).toBeNull();expect(call).toHaveBeenCalledOnce()
})
test('startup denial and a later operation error use the same dismissible dialog',async()=>{
 const failure={code:'shared_folder_permission',details:issue}
 const call=vi.fn(async(method:string)=>{
  if(method==='storage.read')throw failure
  return {account:'ExampleApp',folders:[{key:'media',name:'Media',exists:true,required:'write',granted:'none',state:'pending'},{key:'backup',name:'Backup',exists:true,required:'read',granted:'none',state:'pending'}]}
 })
 const permission=createSharedFolderPermission({call:call as never,locale:'en'})
 mount(permission);await settle()
 let dialog=document.querySelector('[role=dialog]')!
 expect(dialog.textContent).toContain('Media');expect(dialog.textContent).toContain('Backup')
 expect(dialog.textContent).toContain('ExampleApp');expect(dialog.textContent).toContain('Read/Write')
 expect(dialog.textContent).toContain('System internal user')
 ;Array.from(dialog.querySelectorAll('button')).find(b=>b.textContent==='Dismiss')!.click();await settle()
 expect(document.querySelector('[role=dialog]')).toBeNull();expect(call).toHaveBeenCalledOnce()
 await expect(permission.call('storage.read')).rejects.toBe(failure);await settle()
 dialog=document.querySelector('[role=dialog]')!
 expect(dialog.textContent).toContain('Media');expect(call).toHaveBeenCalledTimes(2)
})
test('unmount while checking never opens an orphan dialog',async()=>{
 let answer:(value:unknown)=>void=()=>{}
 const permission=createSharedFolderPermission({call:(()=>new Promise(r=>{answer=r})) as never,locale:'en'})
 mount(permission);cleanups.pop()!()
 answer({account:'ExampleApp',folders:[]});permission.handle({code:'shared_folder_permission',details:issue});await settle()
 expect(document.querySelector('[role=dialog]')).toBeNull()
})
test('retry stays open on denial or request failure and closes after access is granted',async()=>{
 let state='pending'
 const call=vi.fn(async()=>{
  if(state==='error')throw {code:'network'}
  return {account:'ExampleApp',folders:state==='ready'?[]:[{key:'media',name:'Media',exists:true,required:'write',granted:'none',state:'pending'}]}
 })
 mount(createSharedFolderPermission({call:call as never,locale:'en'}));await settle()
 const retry=()=>Array.from(document.querySelectorAll('button')).find(b=>b.textContent==='Retry')!.click()
 retry();await settle()
 expect(document.querySelector('[role=dialog]')).not.toBeNull()
 state='error';retry();await settle()
 expect(document.querySelector('[role=alert]')?.textContent).toContain('could not be read')
 state='ready';retry();await settle()
 expect(document.querySelector('[role=dialog]')).toBeNull()
 expect(call).toHaveBeenCalledTimes(4)
})
