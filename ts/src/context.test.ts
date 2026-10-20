import {readFileSync} from 'node:fs'
import {expect,expectTypeOf,test} from 'vitest'
import {DEFAULT_REQUEST_TIMEOUT,clientTimeout,isFullPage,type Context,type HostAdapter,type HostPlatformSnapshot} from './index'

function ctx(fields:Partial<Context>,platform:Partial<HostPlatformSnapshot>={host:'dsm',dsmStandalone:false}):Context{
 const host={platform:()=>({web:true,host:'dsm',dsmStandalone:false,dsmLanguage:null,appLocale:null,systemVersion:null,fullVersion:null,theme:null,...platform})} as unknown as HostAdapter
 return {call:async()=>undefined as never,reopen:()=>()=>{},locale:'en',host,...fields}
}

// The CLI's DSM window passes them; a full page has no title bar to name the App.
test('the context carries the launch layout and the brand from the DSM window',()=>{
 expectTypeOf<Context['fullPage']>().toEqualTypeOf<boolean|undefined>()
 expectTypeOf<Context['appIcon']>().toEqualTypeOf<string|undefined>()
 expectTypeOf<Context['displayName']>().toEqualTypeOf<string|undefined>()
})

// The CLI's DSM window also passes its AppWindow, DSM's language code and the
// translator of the App's own catalogs.
test('the context carries the DSM window, its language and the App translator',()=>{
 expectTypeOf<Context['appWindow']>().toEqualTypeOf<unknown>()
 expectTypeOf<Context['lang']>().toEqualTypeOf<string|undefined>()
 expectTypeOf<Context['t']>().toEqualTypeOf<((key:string,params?:Record<string,unknown>)=>string)|undefined>()
})

test("a launch is full-page exactly when the DSM window says so",()=>{
 expect(isFullPage(ctx({fullPage:true}))).toBe(true)
 expect(isFullPage(ctx({fullPage:false},{host:'standalone-web',dsmStandalone:null}))).toBe(false)
})

// Older CLIs pass no fullPage: only a DSM desktop window is not a full page.
test('without fullPage only a DSM desktop window is not a full page',()=>{
 expect(isFullPage(ctx({}))).toBe(false)
 expect(isFullPage(ctx({},{host:'dsm',dsmStandalone:null}))).toBe(false)
 expect(isFullPage(ctx({},{host:'dsm',dsmStandalone:true}))).toBe(true)
 expect(isFullPage(ctx({},{host:'standalone-web',dsmStandalone:null}))).toBe(true)
 expect(isFullPage(ctx({},{host:'unavailable',dsmStandalone:null}))).toBe(true)
})

// A dev page or a test that builds its own context without either gets told what is missing.
test('isFullPage without fullPage or host names both',()=>{
 expect(()=>isFullPage({} as never)).toThrow(/fullPage.*host|host.*fullPage/)
 expect(()=>isFullPage({} as never)).not.toThrow(TypeError)
})

// spec/protocol/v1/timeouts.json: the browser waits past the backend's deadline.
test('the request timeout and the client margin are the spec timeouts',()=>{
 const spec=JSON.parse(readFileSync(new URL('../../spec/protocol/v1/timeouts.json',import.meta.url),'utf8'))
 expect(DEFAULT_REQUEST_TIMEOUT).toBe(spec.request.default_s*1000)
 expect(clientTimeout()-DEFAULT_REQUEST_TIMEOUT).toBe(spec.browser.margin_ms)
 expect(clientTimeout(900000)).toBe(900000+spec.browser.margin_ms)
})
