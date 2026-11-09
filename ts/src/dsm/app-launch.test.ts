import {afterEach,expect,expectTypeOf,test,vi} from 'vitest'
import {createDsmAppLauncher,openApp,dsmAppLaunchURL,dsmLaunchURL,createStandaloneHost,
 MAX_DSM_APP_PARAMS,MAX_DSM_APP_PARAM_LENGTH,MAX_DSM_APP_LAUNCH_PARAM,
 type DsmDesktop,type DsmAppTarget,type DsmAppLauncher,type DsmAppLaunchResult} from '../index'

const target:DsmAppTarget={appId:'SYNO.SDS.MailPlusServer.Instance'}
afterEach(()=>vi.unstubAllGlobals())

test('the platform adapter dispatches exactly once with unencoded copied params and its desktop receiver',async()=>{
 const params={route:'/設定/a=b&c #1%',view:'overview'}
 const AppLaunch=vi.fn(function(this:DsmDesktop,id:string,args?:Readonly<Record<string,string>>) {
  expect(this).toBe(desktop)
  expect(id).toBe(target.appId)
  expect(args).toEqual(params)
  expect(args).not.toBe(params)
 })
 const desktop:DsmDesktop={AppLaunch}
 const launcher=createDsmAppLauncher({desktop})
 expectTypeOf(launcher).toEqualTypeOf<DsmAppLauncher>()
 expectTypeOf<ReturnType<DsmAppLauncher['openApp']>>().toEqualTypeOf<Promise<DsmAppLaunchResult>>()
 expect(await launcher.openApp({...target,params})).toEqual({status:'requested'})
 expect(AppLaunch).toHaveBeenCalledTimes(1)
})

test('absent desktop capability is unsupported and never inspects parent or top globals',async()=>{
 const parent=vi.fn(()=>{throw new Error('cross origin')})
 const forbidden=new Proxy({}, {get:parent})
 vi.stubGlobal('parent',forbidden)
 vi.stubGlobal('top',forbidden)
 vi.stubGlobal('SYNO',undefined)
 expect(await openApp(target)).toEqual({status:'unsupported'})
 expect(await openApp(target,{desktop:{}})).toEqual({status:'unsupported'})
 expect(parent).not.toHaveBeenCalled()
})

test('only this page supplies the default desktop; explicit undefined suppresses discovery',async()=>{
 const AppLaunch=vi.fn()
 vi.stubGlobal('SYNO',{SDS:{AppLaunch}})
 expect(await openApp(target)).toEqual({status:'requested'})
 expect(await openApp(target,{desktop:undefined})).toEqual({status:'unsupported'})
 expect(AppLaunch).toHaveBeenCalledTimes(1)
 // Existing browser/native HostAdapter does not pretend to have this capability.
 expect(createStandaloneHost().openApp(target.appId)).toEqual({status:'unsupported'})
})

test.each(['isAppEnabled','isAppLaunchable'] as const)('explicit %s=false is unavailable',async method=>{
 const AppLaunch=vi.fn(),check=vi.fn(()=>false)
 expect(await openApp(target,{desktop:{AppLaunch,StatusNotifier:{[method]:check}}})).toEqual({status:'unavailable'})
 expect(check).toHaveBeenCalledWith(target.appId)
 expect(AppLaunch).not.toHaveBeenCalled()
})

test('missing availability checks do not invent a denial; AppLaunch return values prove no completion',async()=>{
 for(const value of [undefined,false,true,{success:true}]) {
  const AppLaunch=vi.fn(()=>value)
  expect(await openApp(target,{desktop:{AppLaunch}})).toEqual({status:'requested'})
  expect(AppLaunch).toHaveBeenCalledExactlyOnceWith(target.appId,{})
 }
})

test.each([undefined,{},Object.assign(Object.create(null),{route:'/settings'})])('DSM can inspect and extend launch config after dispatch (%j)',async params=>{
 const failures:unknown[]=[],windows:unknown[]=[]
 let completion=Promise.resolve()
 const AppLaunch=vi.fn((id:string,config?:Readonly<Record<string,string>>)=>{
  // DSM waits for its scripts/task button, calls config.hasOwnProperty directly,
  // and swallows a launch exception without creating an App instance.
  completion=Promise.resolve().then(()=>{
   expect(config!.hasOwnProperty('autoCenter')).toBe(false)
   expect(Object.getPrototypeOf(config)).toBe(Object.prototype)
   Object.assign(config!,{dsm_version:'7.3'})
   windows.push({id,config})
  }).catch(error=>{failures.push(error)})
  return completion
 })
 const input=params===undefined?undefined:Object.freeze(params)
 expect(await openApp({...target,params:input},{desktop:{AppLaunch}})).toEqual({status:'requested'})
 await completion
 expect(failures).toEqual([])
 expect(windows).toHaveLength(1)
 expect(AppLaunch).toHaveBeenCalledTimes(1)
 expect(AppLaunch.mock.calls[0]![1]).not.toBe(input)
 expect(input?.dsm_version).toBeUndefined()
})

test('launch and status exceptions return failed without retry or leaking error details',async()=>{
 const AppLaunch=vi.fn(()=>{throw new Error('private host details')})
 expect(await openApp(target,{desktop:{AppLaunch}})).toEqual({status:'failed'})
 expect(AppLaunch).toHaveBeenCalledTimes(1)
 const launch=vi.fn()
 expect(await openApp(target,{desktop:{AppLaunch:launch,StatusNotifier:{isAppEnabled:()=>{throw new Error('private')}}}})).toEqual({status:'failed'})
 expect(launch).not.toHaveBeenCalled()
})

const invalid:unknown[]=[null,{}, {appId:1}, {appId:''}, {appId:'SYNO.SDS.X\n'},
 {appId:'SYNO SDS.X'}, {appId:'X'.repeat(257)}, {appId:'SYNO.SDS.X&other=1'},
 {...target,params:[]}, {...target,params:null}, {...target,params:new Date()}, {...target,params:{route:1}},
 {...target,params:{'bad key':'x'}}, {...target,params:{'a\n':'x'}},
 {...target,params:{route:'x'.repeat(MAX_DSM_APP_PARAM_LENGTH+1)}},
 {...target,params:{route:'\ud800'}}, {...target,params:{route:'\n'}},
 {...target,params:{route:'\u009f'}}, {...target,params:{route:'\u0000'}},
 {...target,params:Object.fromEntries(Array.from({length:MAX_DSM_APP_PARAMS+1},(_,i)=>[`k${i}`,'x']))},
 {...target,params:Object.fromEntries(Array.from({length:8},(_,i)=>[`k${i}`,'x'.repeat(2048)]))},
 ...['password','passwd','pwd','SynoToken','access_token','Authorization','client-secret','api.key','sid'].map(key=>({...target,params:{[key]:'private'}})),
 ...['className','instanceName','dsm_version','title'].map(key=>({...target,params:{[key]:'override'}})),
 ...['hasOwnProperty','__proto__'].map(key=>({...target,params:{[key]:'override'}})),
]
test.each(invalid.map((input,i)=>({input,i})))('invalid target $i cannot reach the host or URL',async({input})=>{
 const AppLaunch=vi.fn()
 expect(await openApp(input as DsmAppTarget,{desktop:{AppLaunch}})).toEqual({status:'invalid-input'})
 expect(AppLaunch).not.toHaveBeenCalled()
 expect(()=>dsmAppLaunchURL('https://dsm.example:5001',input as DsmAppTarget)).toThrow()
})

test('application links round-trip DSM double decoding for punctuation and Unicode without deriving package IDs',()=>{
 const params={route:'/設定/a=b&c #1%',view:'x+y?z'}
 const url=new URL(dsmAppLaunchURL('https://dsm.example:5001',{...target,params}))
 expect(url.origin+url.pathname).toBe('https://dsm.example:5001/')
 expect(url.searchParams.get('launchApp')).toBe(target.appId)
 const encoded=url.searchParams.get('launchParam')!
 expect(Object.fromEntries(encoded.split('&').map(pair=>pair.split('=').map(decodeURIComponent)))).toEqual(params)
 expect(new URL(dsmAppLaunchURL('http://dsm.example',target)).searchParams.get('launchParam')).toBe('')
 expect(dsmLaunchURL('https://dsm.example:5001','HelloGo',params))
  .toBe(dsmAppLaunchURL('https://dsm.example:5001',{appId:'SYNO.SDS.HelloGo.Instance',params}))
})

test('parameter bounds accept their limits and account for encoded expansion',async()=>{
 const AppLaunch=vi.fn()
 for(const params of [{route:'x'.repeat(MAX_DSM_APP_PARAM_LENGTH)},
  Object.fromEntries(Array.from({length:MAX_DSM_APP_PARAMS},(_,i)=>[`k${i}`,'x'])),
  {x:'x'.repeat(2048),y:'x'.repeat(2048),z:'x'.repeat(2048),w:'x'.repeat(2037)}]) {
  expect(await openApp({...target,params},{desktop:{AppLaunch}})).toEqual({status:'requested'})
 }
 const longest=new URL(dsmAppLaunchURL('https://dsm.example',{...target,params:{x:'x'.repeat(2048),y:'x'.repeat(2048),z:'x'.repeat(2048),w:'x'.repeat(2037)}}))
 expect(longest.searchParams.get('launchParam')).toHaveLength(MAX_DSM_APP_LAUNCH_PARAM)
 expect(()=>dsmAppLaunchURL('https://dsm.example',{...target,params:{x:'x'.repeat(2048),y:'x'.repeat(2048),z:'x'.repeat(2048),w:'x'.repeat(2038)}})).toThrow(/long/)
 expect(()=>dsmAppLaunchURL('https://dsm.example',{...target,params:{route:'界'.repeat(1000)}})).toThrow(/long/)
})

test.each(['','/','//dsm.example','https:dsm.example','https:///dsm.example','https://[invalid',
 'javascript:alert(1)','file:///tmp','https://user:private@dsm.example',
 'https://dsm.example/portal/','https://dsm.example?token=private','https://dsm.example#secret',
 ' https://dsm.example','https://dsm.example\n','https://dsm.example\u0000','https://dsm.example\\'])('links reject invalid or ambiguous DSM origin %j',origin=>{
 expect(()=>dsmAppLaunchURL(origin,target)).toThrow()
})
