// @vitest-environment jsdom
import {expect,test,vi,afterEach} from 'vitest'
import {createFileTransfers} from './transfers'
afterEach(()=>{vi.unstubAllGlobals();vi.restoreAllMocks()})
const response=(data:unknown)=>new Response(JSON.stringify(data))

test('upload sends multipart bytes, no overwrite and the current browser token directly to DSM',async()=>{
 const fetch=vi.fn(async()=>response({success:true,data:{}}));vi.stubGlobal('fetch',fetch)
 let token='first'
 const t=createFileTransfers({endpoint:'https://untrusted.invalid/cgi',token:()=>token})
 const file=new File(['payload'],'報告.txt'),signal=new AbortController().signal
 await t.upload('/home',file,signal);token='renewed';await t.upload('/home',file,signal)
 for(const [url,request] of fetch.mock.calls as any){
  expect(url).toBe('/webapi/entry.cgi');expect(request.body).toBeInstanceOf(FormData)
  expect(request.body.get('file')).toBe(file);expect(request.body.get('path')).toBe('/home')
  expect(request.body.get('overwrite')).toBe('false');expect(request.body.get('create_parents')).toBe('false')
  expect(request.credentials).toBe('same-origin');expect(request.mode).toBe('same-origin');expect(request.redirect).toBe('error')
 }
 expect((fetch.mock.calls[1] as any)[1].body.get('SynoToken')).toBe('renewed');t.dispose()
})
test('native download submits the DSM token in an isolated same-origin POST, never a URL',async()=>{
 let action='',method='',token='',path='',policy=''
 const descriptor=Object.getOwnPropertyDescriptor(HTMLIFrameElement.prototype,'contentDocument')!
 vi.spyOn(HTMLIFrameElement.prototype,'contentDocument','get').mockImplementation(function(this:HTMLIFrameElement){
  const doc=descriptor.get!.call(this) as Document|null
  if(doc)vi.spyOn((doc.defaultView as any).HTMLFormElement.prototype,'submit').mockImplementation(function(this:HTMLFormElement){
   action=this.action;method=this.method;token=(this.elements.namedItem('SynoToken') as HTMLInputElement).value
   path=(this.elements.namedItem('path') as HTMLInputElement).value
   policy=doc.head.querySelector('meta')!.content
  })
  return doc
 })
 const transfers=createFileTransfers({endpoint:'https://untrusted.invalid/relay',token:()=> 'secret'})
 await transfers.download('/home/報告.txt')
 expect(new URL(action).pathname).toBe('/webapi/entry.cgi');expect(new URL(action).search).toBe('')
 expect(action).not.toContain('secret');expect(method).toBe('post');expect(token).toBe('secret');expect(JSON.parse(path)).toEqual(['/home/報告.txt'])
 expect(policy).toContain("form-action 'self'")
 const frame=document.querySelector('iframe')!;expect(frame.contentDocument!.querySelector('form')).toBeNull()
 expect(frame.getAttribute('sandbox')).not.toContain('allow-scripts')
 transfers.dispose();expect(document.querySelector('iframe')).toBeNull()
})
test('a lost or malformed upload response is uncertain; DSM permission refusals stay actionable',async()=>{
 vi.stubGlobal('fetch',vi.fn(async()=>{throw Error('offline')}))
 const t=createFileTransfers({token:()=> 'token'}),file=new File(['a'],'a')
 await expect(t.upload('/home',file,new AbortController().signal)).rejects.toEqual({code:'commit-uncertain'})
 vi.stubGlobal('fetch',vi.fn(async()=>response({success:false,error:{code:119}})))
 await expect(t.upload('/home',file,new AbortController().signal)).rejects.toEqual({code:'session_required'})
 vi.stubGlobal('fetch',vi.fn(async()=>response({success:true,data:{errors:[{code:408}]}})))
 await expect(t.upload('/home',file,new AbortController().signal)).rejects.toEqual({code:'denied'});t.dispose()
})
test('invalid paths, pre-canceled uploads and disposed transfers never dispatch',async()=>{
 const fetch=vi.fn();vi.stubGlobal('fetch',fetch)
 const t=createFileTransfers({token:()=> 'token'}),c=new AbortController();c.abort()
 await expect(t.upload('/home',new File(['a'],'a'),c.signal)).rejects.toEqual({code:'canceled'})
 await expect(t.download('/home/../etc')).rejects.toEqual({code:'invalid-request'})
 t.dispose();await expect(t.download('/home/a')).rejects.toEqual({code:'canceled'})
 expect(fetch).not.toHaveBeenCalled()
})
test('a real browser AbortError after upload dispatch stays commit-uncertain',async()=>{
 const fetch=vi.fn((_url:unknown,init?:RequestInit)=>new Promise<Response>((_resolve,reject)=>{
  init!.signal!.addEventListener('abort',()=>reject(new DOMException('aborted','AbortError')),{once:true})
 }))
 vi.stubGlobal('fetch',fetch)
 const t=createFileTransfers({token:()=> 'token'}),controller=new AbortController()
 const pending=t.upload('/home',new File(['bytes'],'a'),controller.signal)
 await vi.waitFor(()=>expect(fetch).toHaveBeenCalledTimes(1))
 controller.abort()
 await expect(pending).rejects.toEqual({code:'commit-uncertain'})
 t.dispose()
})
