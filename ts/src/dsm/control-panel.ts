// Opening a page of DSM Control Panel (SYNO.SDS.AdminCenter.Application).
// Control Panel hands its launch config to the page's activate(): `fn` picks
// the page. Shared Folder (SYNO.SDS.AdminCenter.Share.Main) also reads
// `c2share_name` and, once its list loads, selects and expands that share's
// row (the list is keyed by share name). This works whether Control Panel is
// already open or not. Its Edit dialog needs a loaded list record, so launch
// parameters cannot open it: the administrator clicks Edit themselves.

import {requestDsmApp,type DsmAppTarget,type DsmAppLaunchResult,type DsmAppLauncherOptions} from './app-launch'

export const CONTROL_PANEL_APP='SYNO.SDS.AdminCenter.Application'

export type ControlPanelPage=
 | {page:'shared-folder';select?:string}
 | {page:'login-portal';tab?:'applications'}

export const MAX_SHARE_NAME_LENGTH=255
const shareControls=/[\u0000-\u001f\u007f-\u009f]/

/** A DSM shared-folder name: non-empty, no path separator, no control character. */
function shareName(name:unknown):name is string {
 return typeof name==='string' && name.length>0 && name.length<=MAX_SHARE_NAME_LENGTH &&
  name!=='.' && name!=='..' && !/[\\/]/.test(name) && !shareControls.test(name)
}

/** The launch target for a Control Panel page. Throws on an invalid page or share name, like dsmAppLaunchURL. */
export function controlPanelTarget(p:ControlPanelPage):DsmAppTarget {
 if(p?.page==='shared-folder') {
  if(p.select===undefined) return {appId:CONTROL_PANEL_APP,params:{fn:'SYNO.SDS.AdminCenter.Share.Main'}}
  if(!shareName(p.select)) throw new Error('Invalid shared folder name')
  return {appId:CONTROL_PANEL_APP,params:{fn:'SYNO.SDS.AdminCenter.Share.Main',c2share_name:p.select}}
 }
 if(p?.page==='login-portal') {
  if(p.tab===undefined) return {appId:CONTROL_PANEL_APP,params:{fn:'SYNO.SDS.AdminCenter.Login.Main'}}
  if(p.tab!=='applications') throw new Error('Invalid Control Panel tab')
  return {appId:CONTROL_PANEL_APP,params:{fn:'SYNO.SDS.AdminCenter.Login.Main',tab:p.tab}}
 }
 throw new Error('Invalid Control Panel page')
}

/** Open a Control Panel page in this page's DSM desktop. Invalid input is invalid-input, never a throw. */
export async function openControlPanel(p:ControlPanelPage,options:DsmAppLauncherOptions={}):Promise<DsmAppLaunchResult> {
 let target:DsmAppTarget
 try {target=controlPanelTarget(p)} catch {return {status:'invalid-input'}}
 return requestDsmApp(target,options)
}
