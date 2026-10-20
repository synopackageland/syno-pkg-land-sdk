import {expect,expectTypeOf,test} from 'vitest'
import {MAX_LAUNCH_ROUTE,dsmLaunchURL,followLaunchRoute,launchRoute,type Context,type LaunchParams} from './index'

test('the context carries the launch params and the later launches of the DSM window',()=>{
 expectTypeOf<Context['launchParams']>().toEqualTypeOf<LaunchParams|undefined>()
 expectTypeOf<Context['onLaunch']>().toEqualTypeOf<((fn:(params:LaunchParams)=>void)=>()=>void)|undefined>()
})

test('a route is a path from the App root, the same one a portal link puts after its #',()=>{
 expect(launchRoute({route:'/a/notes/42'})).toBe('/a/notes/42')
 expect(launchRoute({route:'/a/notes/42?tab=history'})).toBe('/a/notes/42?tab=history')
 expect(launchRoute({route:'/'.padEnd(MAX_LAUNCH_ROUTE,'x')})).toHaveLength(MAX_LAUNCH_ROUTE)
})

test('no route, or a route that could leave the App, gives undefined',()=>{
 for(const route of [undefined,'','a/notes','//evil.example/x','/\\evil','https://evil.example/','javascript:alert(1)','/a\nb','/a\u0000b','/'.padEnd(MAX_LAUNCH_ROUTE+1,'x')])
  expect(launchRoute(route===undefined?{}:{route})).toBeUndefined()
 expect(launchRoute(undefined)).toBeUndefined()
})

test('following the launch route navigates now and on each later launch with a route, until stopped',()=>{
 const seen:string[]=[],launches:Array<(params:LaunchParams)=>void>=[]
 let stopped=0
 const ctx={launchParams:{route:'/a/notes/1'},onLaunch:(fn:(params:LaunchParams)=>void)=>{launches.push(fn);return ()=>{stopped++}}}
 const stop=followLaunchRoute(ctx,route=>seen.push(route))
 launches[0]!({route:'/a/notes/2'})
 launches[0]!({page:'x'})
 launches[0]!({route:'//evil.example'})
 expect(seen).toEqual(['/a/notes/1','/a/notes/2'])
 stop()
 expect(stopped).toBe(1)
})

test('a context without launch support, as outside DSM or from an older CLI, follows nothing',()=>{
 const seen:string[]=[]
 const stop=followLaunchRoute({},route=>seen.push(route))
 stop()
 expect(seen).toEqual([])
})

// DSM's desktop url-decodes the launchParam query value, splits it at & and =,
// then url-decodes each key and value (nas.example DSM 7.4-90069, SYNO.SDS.init).
test('a desktop link opens the App window with its params encoded the way DSM decodes them',()=>{
 const url=new URL(dsmLaunchURL('https://nas.example:5001','StudioOffice',{route:'/a/sheet/r=1&x'}))
 expect(url.origin+url.pathname).toBe('https://nas.example:5001/')
 expect(url.searchParams.get('launchApp')).toBe('SYNO.SDS.StudioOffice.Instance')
 const decoded=Object.fromEntries(url.searchParams.get('launchParam')!.split('&').map(pair=>pair.split('=').map(decodeURIComponent)))
 expect(decoded).toEqual({route:'/a/sheet/r=1&x'})
})

test('a desktop link refuses a package name that is not a DSM package id',()=>{
 expect(()=>dsmLaunchURL('https://nas.example:5001','Bad.Name',{})).toThrow(/package/)
})
