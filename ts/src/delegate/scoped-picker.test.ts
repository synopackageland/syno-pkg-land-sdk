import {expect,test,vi} from 'vitest'
import {scopedPicker} from './scoped-picker'
import type {NasProvider} from '../nas/contract'
test('the picker exposes only the confirmed root and refuses traversal before contacting DSM',async()=>{
 const entry={name:'reports',path:'/reports',isDir:true},signal=new AbortController().signal
 const source={info:vi.fn(async()=>entry),shares:vi.fn(),list:vi.fn(async()=>({entries:[entry],offset:0,total:1})),createFolder:vi.fn(),dispose:vi.fn()} as NasProvider
 const picker=scopedPicker(source,'/reports')
 expect((await picker.shares(0,signal)).entries).toEqual([entry]);expect(source.shares).not.toHaveBeenCalled()
 for(const path of ['/reports2','/reports/../other','/other'])await expect(picker.list(path,{},signal)).rejects.toMatchObject({code:'SCOPE_DENIED'})
 expect(source.list).not.toHaveBeenCalled();await picker.list('/reports',{},signal);expect(source.list).toHaveBeenCalledOnce()
 picker.dispose();expect(source.dispose).not.toHaveBeenCalled()
})
