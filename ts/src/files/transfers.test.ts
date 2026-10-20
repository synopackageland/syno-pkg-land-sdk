// @vitest-environment jsdom
import {expect,test,vi,afterEach} from 'vitest'
import {createFileTransfers} from './transfers'
afterEach(()=>vi.unstubAllGlobals())
test('upload passes the File itself with caller credentials and a header token',async()=>{
 const fetch=vi.fn(async()=>({json:async()=>({success:true,data:{uploaded:true}})}));vi.stubGlobal('fetch',fetch)
 const file=new File(['payload'],'\u5831\u544a.txt'),signal=new AbortController().signal
 await createFileTransfers({endpoint:'/cgi/api.cgi',token:()=> 'caller-token'}).upload('/home',file,signal)
 const [url,request]=fetch.mock.calls[0] as any
 expect(url).toContain('method=nas.files.upload');expect(url).toContain('parent=%2Fhome')
 expect(request.body).toBe(file);expect(request.signal).toBe(signal);expect(request.credentials).toBe('same-origin');expect(request.headers['X-SYNO-TOKEN']).toBe('caller-token')
})
test('native download sends the caller token in a POST body, never a URL',()=>{
 let action='',method='',token=''
 const submit=vi.spyOn(HTMLFormElement.prototype,'submit').mockImplementation(function(this:HTMLFormElement){action=this.action;method=this.method;token=(this.elements.namedItem('SynoToken') as HTMLInputElement).value})
 const transfers=createFileTransfers({endpoint:'/cgi/api.cgi',token:()=> 'secret'})
 transfers.download('/home/\u5831\u544a.txt')
 expect(action).toContain('method=nas.files.download');expect(action).not.toContain('secret');expect(method).toBe('post');expect(token).toBe('secret')
 expect(document.querySelector('form')).toBeNull();transfers.dispose();expect(document.querySelector('iframe')).toBeNull();submit.mockRestore()
})
test('lost upload response is uncertain and server DSM refusal stays actionable',async()=>{
 vi.stubGlobal('fetch',vi.fn(async()=>{throw Error('offline')}))
 const t=createFileTransfers({endpoint:'/cgi/api.cgi'}),file=new File(['a'],'a')
 await expect(t.upload('/home',file,new AbortController().signal)).rejects.toEqual({code:'commit-uncertain'})
 vi.stubGlobal('fetch',vi.fn(async()=>({json:async()=>({success:false,error:{code:'dsm_session_required'}})})))
 await expect(t.upload('/home',file,new AbortController().signal)).rejects.toEqual({code:'dsm_session_required'})
})
