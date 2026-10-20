import {expect,test,vi} from 'vitest'
import {createClient} from '../client'
import {createSharedFolderPermission,sharedFolderPermissionMessage} from './permission'

const issue={account:'ExampleApp',share:'Media',required:'write' as const}
const pending={account:issue.account,folders:[{key:'media',name:'Media',exists:true,required:'write',granted:'read',state:'pending'}]}
test('concurrent and later startup checks make exactly one request',async()=>{
 const call=vi.fn(async()=>pending)
 const permission=createSharedFolderPermission({call:call as never,locale:'en'})
 const events=vi.fn();permission.subscribe(events)
 await Promise.all([permission.check(),permission.check()]);await permission.check()
 expect(call).toHaveBeenCalledExactlyOnceWith('sharedfolder.status',{})
 expect(events).toHaveBeenLastCalledWith([issue])
 expect(sharedFolderPermissionMessage(issue,'en')).toContain('System internal user')
 expect(sharedFolderPermissionMessage(issue,'cht')).toContain('系統內部使用者')
})

test('structured wire errors trigger the same notification and remain rejected',async()=>{
 const error={code:'shared_folder_permission',message:'grant access',details:issue}
 const fetch=vi.fn(async()=>({json:async()=>({success:false,error})}))
 const client=createClient({endpoint:'/api.cgi',fetch:fetch as never})
 const permission=createSharedFolderPermission({call:client.call,locale:'en'})
 const events=vi.fn();const stop=permission.subscribe(events)
 await expect(permission.call('library.save',{text:'x'})).rejects.toEqual(error)
 expect(events).toHaveBeenLastCalledWith([issue])
 expect(permission.handle({code:'shared_folder_permission',details:{...issue,required:'root'}})).toBe(false)
 expect(permission.handle({code:'internal'})).toBe(false)
 const count=events.mock.calls.length;stop();permission.handle(error)
 expect(events).toHaveBeenCalledTimes(count)
})

test('a late startup result cannot clear a real I/O failure',async()=>{
 let answer:(value:unknown)=>void=()=>{}
 const permission=createSharedFolderPermission({call:(()=>new Promise(r=>{answer=r})) as never,locale:'en'})
 const events=vi.fn();permission.subscribe(events)
 const checked=permission.check()
 permission.handle({code:'shared_folder_permission',details:issue})
 answer({account:issue.account,folders:[]});await checked
 expect(events).toHaveBeenLastCalledWith([issue])
})

test('a failed check is not retried and never fabricates a permission problem',async()=>{
 const call=vi.fn(async()=>{throw {code:'shared_folder_identity'}})
 const permission=createSharedFolderPermission({call:call as never,locale:'en'})
 const events=vi.fn();permission.subscribe(events)
 await expect(permission.check()).rejects.toMatchObject({code:'shared_folder_unavailable'})
 await expect(permission.check()).rejects.toMatchObject({code:'shared_folder_unavailable'})
 expect(call).toHaveBeenCalledOnce();expect(events).not.toHaveBeenCalled()
})
