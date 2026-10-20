import {expect,test,vi} from 'vitest'
import {createBackgroundSaveFacade} from './facade'
test('destination lookup returns protected selection and rejects a grant outside its root',async()=>{
 const selection={scope:'shared:reports',root:'/reports',generation:2}
 const destination={id:'mine',key:'save',path:'/reports/exports',username:'alice',lifetime:'until-revoked'}
 const call=vi.fn(async()=>({key:'save',selection,destination,status:'active'}))
 const sdk=createBackgroundSaveFacade({call:call as any})
 expect(await sdk.destination('save')).toEqual({key:'save',selection,destination,status:'active'})
 expect(call).toHaveBeenCalledExactlyOnceWith('delegate.destination',{key:'save'})
 call.mockResolvedValue({key:'save',selection,destination:{...destination,path:'/other'},status:'active'})
 await expect(sdk.destination('save')).rejects.toMatchObject({code:'EXECUTOR_PROTOCOL'})
})
test('a destination whose grant was revoked reports revoked',async()=>{
 const selection={scope:'shared:reports',root:'/reports',generation:2}
 const sdk=createBackgroundSaveFacade({call:vi.fn(async()=>({key:'save',selection,destination:null,status:'revoked'})) as any})
 expect(await sdk.destination('save')).toEqual({key:'save',selection,destination:null,status:'revoked'})
})
const ref={delegation_id:'mine',job_id:'original'}
test('operation cancellation keeps its bounded readable cause through the facade',async()=>{
 const call=vi.fn(async()=>({job_id:'original',state:'canceled',code:'OPERATION_CANCELED',reason:'grant-revoked'}))
 const sdk=createBackgroundSaveFacade({call:call as any})
 expect(await sdk.cancel(ref)).toMatchObject({state:'canceled',reason:'grant-revoked'})
 for(const reason of [123, 'x'.repeat(129), 'raw DSM response /path']){
  call.mockResolvedValue({...await call(),reason} as any)
  await expect(sdk.cancel(ref)).rejects.toMatchObject({code:'EXECUTOR_PROTOCOL'})
 }
})
test('a copy cancel acknowledgement passes through unchanged for same-ref polling',async()=>{
 const ack={job_id:'original',state:'copying',code:'CANCEL_REQUESTED',reason:'operation-canceled-by-user'}
 const call=vi.fn(async()=>ack)
 expect(await createBackgroundSaveFacade({call:call as any}).cancel(ref)).toEqual(ack)
 expect(call).toHaveBeenCalledWith('delegate.operations.cancel',ref)
})
const grant={delegation_id:'mine',destination:'save',path:'/home/Exports',subject:{username:'fixtureuser1',uid:1205},lifetime:'until-revoked',revoked:false}
function fixture(){
 const call=vi.fn(async(method:string)=>{
  if(method==='delegate.status')return {state:'ready',disabled:false}
  if(method==='delegate.list')return {delegations:[grant,{...grant,delegation_id:'revoked',revoked:true},{...grant,delegation_id:'legacy',lifetime:''}]}
  if(method==='delegate.operations.get')return {job_id:'original',state:'committed',receipt:{path:'/home/Exports/report.csv',size:4}}
  if(method==='delegate.operations.cancel')return {job_id:'original',state:'canceled'}
  if(method==='delegate.revoke')return {revoked:true}
  if(method==='delegate.disable')return {disabled:true}
  if(method==='delegate.enable')return {disabled:false}
  if(method==='delegate.select_destination')return {id:'mine',key:'save',path:'/home/Exports',username:'fixtureuser1',lifetime:'until-revoked'}
  throw {code:'unknown_method'}
 })
 const jobs=vi.fn(async()=>[{...ref,name:'report.csv'}])
 return {call,jobs,sdk:createBackgroundSaveFacade({call:call as any},jobs)}
}
test('delegate.list is mapped to active permanent destinations without changing owner or legacy semantics',async()=>{
 const {sdk,call}=fixture()
 expect(await sdk.list()).toEqual([{id:'mine',key:'save',path:'/home/Exports',username:'fixtureuser1',lifetime:'until-revoked'}])
 expect(call).toHaveBeenCalledExactlyOnceWith('delegate.list')
 await sdk.selectDestination({key:'save',path:'/home/Exports'})
 expect(call).toHaveBeenLastCalledWith('delegate.select_destination',{key:'save',path:'/home/Exports'})
})
test('App metadata is joined with protected status by the original two IDs, never a synthetic list route',async()=>{
 const {sdk,call,jobs}=fixture()
 expect(await sdk.operations()).toEqual([{...ref,name:'report.csv',state:'committed',receipt:{path:'/home/Exports/report.csv',size:4}}])
 expect(jobs).toHaveBeenCalledOnce()
 expect(call).toHaveBeenCalledExactlyOnceWith('delegate.operations.get',ref)
 await sdk.cancel(ref);expect(call).toHaveBeenLastCalledWith('delegate.operations.cancel',ref)
 await sdk.revoke({delegation_id:'mine'});expect(call).toHaveBeenLastCalledWith('delegate.revoke',{delegation_id:'mine'})
})
test('missing protected disabled status cannot be reported as enabled',async()=>{
 const {sdk,call}=fixture();call.mockResolvedValue({state:'ready'} as any)
 await expect(sdk.status()).rejects.toMatchObject({code:'EXECUTOR_PROTOCOL'})
})
test('malformed grant and App metadata references cannot be coerced into valid IDs',async()=>{
 const {sdk,call}=fixture()
 call.mockResolvedValue({delegations:[{...grant,delegation_id:undefined}]} as any)
 await expect(sdk.list()).rejects.toMatchObject({code:'EXECUTOR_PROTOCOL'})
 const invalid=createBackgroundSaveFacade({call:call as any},async()=>[{job_id:undefined,delegation_id:'mine',name:'report.csv'}] as any)
 await expect(invalid.operations()).rejects.toMatchObject({code:'EXECUTOR_PROTOCOL'})
 expect(call).toHaveBeenCalledTimes(1)
})
test('malformed or mismatched operation evidence never becomes Saved',async()=>{
 for(const value of [{job_id:'other',state:'committed',receipt:{path:'/home/a',size:1}},{job_id:'original',state:'committed'},{job_id:'original',state:'copying',receipt:{path:'/home/a',size:1}},{job_id:'original',state:'committed',receipt:{path:'/home/a',size:-1}}]){
  const {sdk,call}=fixture();call.mockResolvedValue(value as any)
  await expect(sdk.operations()).rejects.toMatchObject({code:'EXECUTOR_PROTOCOL'})
 }
})
test('read timeout never admits or resubmits an operation',async()=>{
 const {sdk,call}=fixture();call.mockRejectedValue({code:'EXECUTOR_TIMEOUT'})
 await expect(sdk.operations()).rejects.toMatchObject({code:'EXECUTOR_TIMEOUT'})
 expect(call).toHaveBeenCalledExactlyOnceWith('delegate.operations.get',ref)
})
test('disable and enable use authenticated protected routes, not mailbox controls',async()=>{
 const {sdk,call}=fixture()
 expect(await sdk.status()).toEqual({disabled:false})
 expect(await sdk.disable()).toEqual({disabled:true});expect(call).toHaveBeenLastCalledWith('delegate.disable')
 expect(await sdk.enable()).toEqual({disabled:false});expect(call).toHaveBeenLastCalledWith('delegate.enable')
 call.mockResolvedValue({} as any);await expect(sdk.disable()).rejects.toMatchObject({code:'EXECUTOR_PROTOCOL'})
})
test('the default facade lists backend mirrors but ignores their committed claim until live receipt',async()=>{
 const {call}=fixture()
 call.mockImplementation(async(method:string)=>{
  if(method==='delegate.operations.list')return {operations:[{...ref,name:'report.csv',state:'committed'}]} as any
  if(method==='delegate.operations.get')return {job_id:'original',state:'uncertain'} as any
  throw {code:'unexpected'}
 })
 const sdk=createBackgroundSaveFacade({call:call as any})
 expect(await sdk.operations()).toEqual([{...ref,name:'report.csv',state:'uncertain'}])
 expect(call.mock.calls).toEqual([['delegate.operations.list'],['delegate.operations.get',ref]])
})
