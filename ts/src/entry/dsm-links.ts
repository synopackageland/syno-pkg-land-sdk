import type {HostAdapter, HostFileNavigationResult, HostRequestResult} from '../context'
import {dsmAppLaunchURL} from '../launch'
import {FILE_STATION_APP, fileStationPath, sharedFolderPath} from '../dsm/file-station'

/** Opens a DSM App in a new tab on the DSM origin; DSM's own launch link
 * carries the params. requested only acknowledges the request. */
export type DsmLinkOpener = (appId: string, params?: Readonly<Record<string, string>>) => HostRequestResult

export function dsmLinkOpener(origin: string, open: (url: string) => unknown = url => globalThis.window?.open(url, '_blank', 'noopener')): DsmLinkOpener {
 return (appId, params) => {
  let url: string
  try {url = dsmAppLaunchURL(`${origin}/`, {appId, params})} catch {return {status: 'invalid-input'}}
  if(typeof globalThis.window === 'undefined') return {status: 'unavailable'}
  try {open(url)} catch {return {status: 'failed'}}
  return {status: 'requested'}
 }
}

/**
 * A full page outside DSM's desktop has no SYNO.SDS.AppLaunch: openApp,
 * openFileManager and revealFile open DSM's own launch link in a new tab
 * instead, with the File Station rule of the DSM window. The rest of the
 * browser host is kept; a native shell keeps its own host.
 */
export function withDsmLinks(host: HostAdapter, open: DsmLinkOpener): HostAdapter {
 if(host.platform().host !== 'standalone-web') return host
 function files(path: string, isDirectory: boolean): HostFileNavigationResult {
  if(typeof path !== 'string' || !path.startsWith('/')) return {status: 'invalid-input'}
  const target = fileStationPath(path)
  if(!sharedFolderPath(target)) return {status: 'invalid-input'}
  const result = open(FILE_STATION_APP, isDirectory ? {opendir: target} : {openfile: target})
  if(result.status !== 'requested') return {status: result.status === 'blocked' ? 'failed' : result.status}
  return {status: 'requested', action: isDirectory ? 'locate-folder' : 'highlight-file'}
 }
 return {...host,
  openApp: appId => open(appId),
  openFileManager: path => files(path, true),
  revealFile: path => files(path, false),
 }
}
