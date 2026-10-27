// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import DelegatePanel from './DelegatePanel.vue'

vi.mock('../nas/NasPicker.vue',()=>({default:{emits:['selected'],setup(_:unknown,{emit}:any){emit('selected','/home/exports');return ()=>null}}}))
const settle=async()=>{for(let i=0;i<15;i++)await nextTick()}
test('the delegate form submits and retains its key after a lost reply on HTTP DSM',async()=>{
 const getRandomValues=crypto.getRandomValues.bind(crypto)
 vi.stubGlobal('crypto',{getRandomValues,randomUUID:undefined,subtle:undefined})
 const call=vi.fn(async(method:string,_params?:unknown):Promise<unknown>=>{
  if(method==='delegate.status')return {state:'ready'}
  if(method==='delegate.list')return {delegations:[]}
  if(method==='delegate.jobs.submit')throw {code:'network'}
  return {jobs:[]}
 })
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(DelegatePanel,{ctx:{call:call as never,locale:'en'}}))})
 try{
  app.mount(root);await settle()
  for(const [selector,value] of [['#delegate-url','https://example.com/file'],['#delegate-name','file']]){
   const input=root.querySelector<HTMLInputElement>(selector)!;input.value=value;input.dispatchEvent(new Event('input',{bubbles:true}))
  }
  await settle()
  for(let i=0;i<2;i++){root.querySelector('form')!.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));await settle()}
  const calls=call.mock.calls.filter(([method])=>method==='delegate.jobs.submit')
  expect(calls).toHaveLength(2)
  expect(calls[0][1]).toMatchObject({key:expect.stringMatching(/^[a-f0-9]{32}$/),dest:'/home/exports'})
  expect(calls[1][1]).toEqual(calls[0][1])
 }finally{app.unmount();root.remove();vi.unstubAllGlobals()}
})
