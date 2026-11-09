// @vitest-environment jsdom
import {afterEach,expect,expectTypeOf,test,vi} from 'vitest'
import {createStandaloneContext,createStandaloneHost,isFullPage,type Context,type HostAdapter} from './index'

afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks();document.title=''})

// A dev page or a test owns the browser page: no DSM window, no casts, no stubs.
test('a standalone host is a browser page outside DSM',()=>{
 const host=createStandaloneHost()
 expectTypeOf(host).toEqualTypeOf<HostAdapter>()
 expect(host.platform()).toEqual({web:true,host:'standalone-web',dsmStandalone:null,dsmLanguage:null,appLocale:null,systemVersion:null,fullVersion:null,theme:null})
 expect(host.activateWindow()).toEqual({status:'unsupported'})
 expect(host.requestClose()).toEqual({status:'unsupported'})
 expect(host.openApp('SYNO.SDS.App.FileStation3.Instance')).toEqual({status:'unsupported'})
 expect(host.openFileManager('/photo')).toEqual({status:'unsupported'})
 expect(host.revealFile('/photo/a.jpg')).toEqual({status:'unsupported'})
 expect(host.openFile('/photo/a.jpg')).toEqual({status:'unsupported'})
 expect(host.showFileDetails(['/photo/a.jpg'])).toEqual({status:'unsupported'})
 expect(host.openAppSetting()).toEqual({status:'unsupported'})
 expect(host.subscribe('host-theme-change',()=>{})).toEqual({status:'unsupported'})
 host.dispose()
})

test('a standalone host titles the document and opens URLs in a new tab',()=>{
 const host=createStandaloneHost()
 expect(host.setTitle('Connect')).toEqual({status:'ok',target:'document'})
 expect(document.title).toBe('Connect')
 const open=vi.spyOn(window,'open').mockReturnValue(null)
 expect(host.openURL('https://example.com/')).toEqual({status:'requested',windowHandleAvailable:false})
 expect(open).toHaveBeenCalledWith('https://example.com/','_blank','noopener')
 expect(host.openURL('javascript:alert(1)')).toEqual({status:'invalid-input'})
})

test('a standalone host reports window resizes until disposed',()=>{
 const host=createStandaloneHost()
 const listener=vi.fn()
 const result=host.subscribe('window-resize',listener)
 expect(result.status).toBe('subscribed')
 window.dispatchEvent(new Event('resize'))
 expect(listener).toHaveBeenCalledOnce()
 host.dispose()
 window.dispatchEvent(new Event('resize'))
 expect(listener).toHaveBeenCalledOnce()
})

test('a standalone host may say it is somewhere else, for a test',()=>{
 const host=createStandaloneHost({host:'dsm',dsmStandalone:false})
 expect(host.platform()).toMatchObject({host:'dsm',dsmStandalone:false,web:true})
})

test('a standalone context is a full page that needs only a call',async()=>{
 const call=vi.fn(async()=>({ok:true})) as unknown as Context['call']
 const ctx=createStandaloneContext({call})
 expectTypeOf(ctx).toEqualTypeOf<Context>()
 expect(ctx).toMatchObject({locale:'en',fullPage:true})
 expect(isFullPage(ctx)).toBe(true)
 expect(ctx.host.platform().host).toBe('standalone-web')
 ctx.reopen(()=>{})()
 await expect(ctx.call('app.status')).resolves.toEqual({ok:true})
 const own=createStandaloneContext({call,locale:'zh-TW',fullPage:false,user:{name:'alice',isAdmin:false}})
 expect(own).toMatchObject({locale:'zh-TW',fullPage:false,user:{name:'alice',isAdmin:false}})
})


test('activation coalesces visible and focus signals, repeats on return and stops on disposal',()=>{
 vi.useFakeTimers()
 const visibility=vi.spyOn(document,'visibilityState','get').mockReturnValue('visible')
 const host=createStandaloneHost(), listener=vi.fn()
 const sub=host.subscribe('window-activated',listener)
 expect(sub.status).toBe('subscribed')
 expect(listener).not.toHaveBeenCalled()
 document.dispatchEvent(new Event('visibilitychange'))
 vi.advanceTimersByTime(10)
 window.dispatchEvent(new Event('focus'))
 vi.advanceTimersByTime(50)
 expect(listener).toHaveBeenCalledTimes(1)
 visibility.mockReturnValue('hidden')
 document.dispatchEvent(new Event('visibilitychange'))
 window.dispatchEvent(new Event('focus'))
 vi.advanceTimersByTime(100)
 expect(listener).toHaveBeenCalledTimes(1)
 visibility.mockReturnValue('visible')
 document.dispatchEvent(new Event('visibilitychange'))
 vi.advanceTimersByTime(50)
 expect(listener).toHaveBeenCalledTimes(2)
 window.dispatchEvent(new Event('focus'))
 if(sub.status==='subscribed') {sub.dispose();sub.dispose()}
 vi.advanceTimersByTime(100)
 expect(listener).toHaveBeenCalledTimes(2)
 window.dispatchEvent(new Event('focus'))
 vi.advanceTimersByTime(100)
 expect(listener).toHaveBeenCalledTimes(2)
 host.dispose()
 expect(host.subscribe('window-activated',listener)).toEqual({status:'unavailable'})
})

test('activation cancels queued delivery on hide or host disposal and isolates subscribers',()=>{
 vi.useFakeTimers()
 const visibility=vi.spyOn(document,'visibilityState','get').mockReturnValue('visible')
 const host=createStandaloneHost(), other=createStandaloneHost(), a=vi.fn(), b=vi.fn()
 host.subscribe('window-activated',a);other.subscribe('window-activated',b)
 window.dispatchEvent(new Event('focus'))
 visibility.mockReturnValue('hidden')
 document.dispatchEvent(new Event('visibilitychange'))
 vi.advanceTimersByTime(100)
 expect(a).not.toHaveBeenCalled();expect(b).not.toHaveBeenCalled()
 visibility.mockReturnValue('visible')
 document.dispatchEvent(new Event('visibilitychange'))
 host.dispose();host.dispose()
 vi.advanceTimersByTime(100)
 expect(a).not.toHaveBeenCalled();expect(b).toHaveBeenCalledTimes(1)
 other.dispose()
})
