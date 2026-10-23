import {expect,test,vi} from 'vitest'
import {nasContext} from '../../test/dsm-files-fixture'
import {createNasProvider} from '../nas/provider'
import {createBackgroundPickerProvider} from '../background/picker-provider'
import {nasCatalogs,nasCopy,nasErrorCopy} from '../nas/locale'
const signal=()=>new AbortController().signal

test('provider forwards pagination/sorting and preserves every DSM-returned path',async()=>{
 const call=vi.fn(async()=>({files:[{name:'outside homes',path:'/team/outside homes',isdir:false,additional:{size:12}}],offset:100,total:102}))
 const p=createNasProvider(nasContext(call))
 const page=await p.list('/team',{offset:100,limit:1,sortBy:'size',sortDirection:'DESC'},signal())
 expect(call).toHaveBeenCalledWith('nas.files.list',{path:'/team',offset:100,limit:1,sortBy:'size',sortDirection:'DESC'})
 expect(page.entries[0]?.path).toBe('/team/outside homes');expect(page.nextOffset).toBe(101)
})
test.each([
 {},{files:[],offset:0,total:1},{files:[],offset:-1,total:0},
 {files:[{name:'x',path:'/x',isdir:true}],offset:0,total:0},
 {files:[{name:'x',path:'/x',isdir:'true'}],offset:0,total:1},
])('malformed or stalled page fails visibly: %j',async(value)=>{
 const p=createNasProvider(nasContext(vi.fn(async()=>value)))
 await expect(p.shares(0,signal())).rejects.toEqual({code:'unavailable'})
})
test('canceling or disposing observation rejects late results and stops subsequent calls',async()=>{
 let resolve!:(v:unknown)=>void
 const call=vi.fn(()=>new Promise(r=>{resolve=r})),p=createNasProvider(nasContext(call)),abort=new AbortController()
 const pending=p.shares(0,abort.signal);await vi.waitFor(()=>expect(call).toHaveBeenCalledTimes(1));abort.abort()
 await expect(pending).rejects.toEqual({code:'canceled'})
 resolve({shares:[],offset:0,total:0});p.dispose()
 await expect(p.shares(0,signal())).rejects.toEqual({code:'canceled'})
 expect(call).toHaveBeenCalledTimes(1)
})
test('lost mutation response stays uncertain and sensitive error details are discarded',async()=>{
 const p=createNasProvider(nasContext(vi.fn(async()=>{throw {message:'SID=secret',code:'transport_error'}})))
 await expect(p.createFolder('/team','new',signal())).rejects.toEqual({code:'commit-uncertain'})
})
test('all supported locales share copy keys, aliases and explicit permission errors',()=>{
 const keys=Object.keys(nasCopy('en')).sort()
 for(const copy of Object.values(nasCatalogs)){
  expect(Object.keys(copy).sort()).toEqual(keys)
  expect(nasErrorCopy({code:'denied',message:'SID=secret'},copy)).toBe(copy.errorDenied)
 }
 expect(nasCopy('cht')).toBe(nasCopy('zh-TW'))
 expect(nasCopy('unknown')).toBe(nasCopy('en'))
})

test('App-only file errors retain a DSM-login recovery hint',async()=>{
 const p=createNasProvider(nasContext(vi.fn(async()=>{throw {code:'dsm_session_required'}})))
 await expect(p.shares(0,signal())).rejects.toEqual({code:'dsm_session_required'})
 expect(nasErrorCopy({code:'dsm_session_required'},nasCopy('en'))).toContain('DSM')
})
test('system folders DSM keeps for itself are never listed, and paging still follows DSM',async()=>{
 const files=['#recycle','#snapshot','@eaDir','@tmp','@sharebin','photos','#recycle-notes'].map(name=>({name,path:`/team/${name}`,isdir:true}))
 const p=createNasProvider(nasContext(vi.fn(async()=>({files,offset:0,total:9}))))
 const page=await p.list('/team',{offset:0,limit:7},signal())
 expect(page.entries.map(e=>e.name)).toEqual(['photos','#recycle-notes'])
 expect(page.nextOffset).toBe(7)
})
test('the client waits two seconds past the App request deadline',async()=>{
 vi.useFakeTimers()
 try{
  const call=vi.fn(()=>new Promise(()=>{}))
  for(const [requestTimeout,wait] of [[undefined,62000],[90000,92000]] as const){
   const p=createNasProvider({...nasContext(call),requestTimeout}),seen=vi.fn()
   const pending=p.shares(0,signal()).catch(seen)
   await vi.advanceTimersByTimeAsync(wait-1);expect(seen).not.toHaveBeenCalled()
   await vi.advanceTimersByTimeAsync(1);await pending
   expect(seen).toHaveBeenCalledWith({code:'timeout'})
  }
 }finally{vi.useRealTimers()}
})
test('a backend deadline or cancel reads as a timeout, and uncertain for a mutation',async()=>{
 for(const [code,read] of [['request_timeout','timeout'],['request_canceled','canceled']]){
  const p=createNasProvider({call:vi.fn(async()=>{throw {code}})})
  await expect(p.shares(0,signal())).rejects.toEqual({code:read})
  await expect(p.createFolder('/team','new',signal())).rejects.toEqual({code:'commit-uncertain'})
 }
})
test('the background picker keeps the App request deadline',async()=>{
 vi.useFakeTimers()
 try{
  const p=createBackgroundPickerProvider({...nasContext(vi.fn(()=>new Promise(()=>{}))),requestTimeout:90000}),seen=vi.fn()
  const pending=p.shares(0,signal()).catch(seen)
  await vi.advanceTimersByTimeAsync(62000);expect(seen).not.toHaveBeenCalled()
  await vi.advanceTimersByTimeAsync(30000);await pending
  expect(seen).toHaveBeenCalledWith({code:'timeout'})
 }finally{vi.useRealTimers()}
})
test('a request the backend refused as cross-origin says so, also for a mutation, in every locale',async()=>{
 for(const code of ['cross_origin','origin_required']){
  const p=createNasProvider({call:vi.fn(async()=>{throw {code}})})
  await expect(p.shares(0,signal())).rejects.toEqual({code:'origin-refused'})
  await expect(p.createFolder('/team','new',signal())).rejects.toEqual({code:'origin-refused'})
 }
 for(const copy of Object.values(nasCatalogs))expect(nasErrorCopy({code:'origin-refused'},copy)).toBe(copy.errorOrigin)
 expect(nasCopy('en').errorOrigin).toContain('browser')
})
