import {afterEach,expect,test,vi} from 'vitest'
import {CONTROL_PANEL_APP,controlPanelTarget,openControlPanel,dsmAppLaunchURL,
 type ControlPanelPage,type DsmDesktop} from '../index'

afterEach(()=>vi.unstubAllGlobals())
const SHARE='SYNO.SDS.AdminCenter.Share.Main',LOGIN='SYNO.SDS.AdminCenter.Login.Main'

function desktop(extra:Partial<DsmDesktop>={}) {
 const calls:unknown[]=[]
 return {calls,desktop:{AppLaunch:(id:string,p?:Readonly<Record<string,string>>)=>{calls.push([id,p])},...extra} as DsmDesktop}
}

test('the app ID is Control Panel',()=>{
 expect(CONTROL_PANEL_APP).toBe('SYNO.SDS.AdminCenter.Application')
})

test.each([
 [{page:'shared-folder'},{fn:SHARE}],
 [{page:'shared-folder',select:'Media'},{fn:SHARE,c2share_name:'Media'}],
 [{page:'shared-folder',select:'家庭 相片 #1&a=b'},{fn:SHARE,c2share_name:'家庭 相片 #1&a=b'}],
 [{page:'login-portal'},{fn:LOGIN}],
 [{page:'login-portal',tab:'applications'},{fn:LOGIN,tab:'applications'}],
] as Array<[ControlPanelPage,Record<string,string>]>)('%j launches Control Panel once with exactly its page',async(page,params)=>{
 expect(controlPanelTarget(page)).toEqual({appId:CONTROL_PANEL_APP,params})
 const {calls,desktop:d}=desktop()
 expect(await openControlPanel(page,{desktop:d})).toEqual({status:'requested'})
 expect(calls).toEqual([[CONTROL_PANEL_APP,params]])
})

test('the default desktop is this page\'s SYNO.SDS',async()=>{
 const AppLaunch=vi.fn()
 vi.stubGlobal('SYNO',{SDS:{AppLaunch}})
 expect(await openControlPanel({page:'shared-folder',select:'Media'})).toEqual({status:'requested'})
 expect(AppLaunch).toHaveBeenCalledExactlyOnceWith(CONTROL_PANEL_APP,{fn:SHARE,c2share_name:'Media'})
})

test.each([
 '','a/b','/Media','a\\b','.','..','a\nb','tab\there','\u0085','x'.repeat(256),7,null,
])('an invalid share name %j is invalid-input and never launches',async select=>{
 const page={page:'shared-folder',select} as unknown as ControlPanelPage
 expect(()=>controlPanelTarget(page)).toThrow()
 const {calls,desktop:d}=desktop()
 expect(await openControlPanel(page,{desktop:d})).toEqual({status:'invalid-input'})
 expect(calls).toEqual([])
})

test('a 255-character share name is accepted',async()=>{
 const {calls,desktop:d}=desktop()
 expect(await openControlPanel({page:'shared-folder',select:'x'.repeat(255)},{desktop:d})).toEqual({status:'requested'})
 expect(calls).toHaveLength(1)
})

test.each([undefined,null,{},{page:'users'},{page:'login-portal',tab:'general'}])('an unknown page or tab %j is invalid-input',async page=>{
 const {calls,desktop:d}=desktop()
 expect(await openControlPanel(page as unknown as ControlPanelPage,{desktop:d})).toEqual({status:'invalid-input'})
 expect(calls).toEqual([])
})

test('there is no edit action: other fields never reach the launch',()=>{
 expect(controlPanelTarget({page:'shared-folder',edit:'Media'} as unknown as ControlPanelPage).params).toEqual({fn:SHARE})
})

test('unsupported and unavailable pass through from the desktop',async()=>{
 vi.stubGlobal('SYNO',undefined)
 expect(await openControlPanel({page:'shared-folder'})).toEqual({status:'unsupported'})
 expect(await openControlPanel({page:'shared-folder'},{desktop:{}})).toEqual({status:'unsupported'})
 expect(await openControlPanel({page:'shared-folder'},{desktop:undefined})).toEqual({status:'unsupported'})
 const check=vi.fn(()=>false)
 const {calls,desktop:d}=desktop({StatusNotifier:{isAppLaunchable:check}})
 expect(await openControlPanel({page:'login-portal',tab:'applications'},{desktop:d})).toEqual({status:'unavailable'})
 expect(check).toHaveBeenCalledWith(CONTROL_PANEL_APP)
 expect(calls).toEqual([])
 const failing={AppLaunch:()=>{throw new Error('boom')}}
 expect(await openControlPanel({page:'shared-folder'},{desktop:failing})).toEqual({status:'failed'})
})

test('the same target makes a DSM link',()=>{
 const url=new URL(dsmAppLaunchURL('https://dsm.example:5001',controlPanelTarget({page:'shared-folder',select:'My Share&x'})))
 expect(url.origin).toBe('https://dsm.example:5001')
 expect(url.searchParams.get('launchApp')).toBe(CONTROL_PANEL_APP)
 expect(url.searchParams.get('launchParam')).toBe(`fn=${encodeURIComponent(SHARE)}&c2share_name=${encodeURIComponent('My Share&x')}`)
 const login=new URL(dsmAppLaunchURL('https://dsm.example:5001/',controlPanelTarget({page:'login-portal',tab:'applications'})))
 expect(login.searchParams.get('launchParam')).toBe(`fn=${encodeURIComponent(LOGIN)}&tab=applications`)
})
