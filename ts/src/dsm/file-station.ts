// Opening a NAS path in DSM's own File Station, as Reclaim verified on DSM 7.4
//: AppLaunch(FILE_STATION_APP, {opendir}) shows a folder and {openfile}
// selects a file in its folder. File Station reads shared-folder paths only;
// it rejects /volume1/... and ignores {path}. There is no completion callback.
//
// Stripping a DSM volume mount (/volume1, /volumeUSB1, /volumeSATA1) cannot
// widen access: File Station runs in the viewer's own DSM session with their
// share permissions, and a path grants nothing. Shared-folder names are unique
// on a NAS, so /volumeN/<share> names one share whatever its volume. A path
// that is not one canonical shared-folder path (relative, a volume root, ., ..,
// an empty segment, a backslash or a control character) never launches.

export const FILE_STATION_APP='SYNO.SDS.App.FileStation3.Instance'

/** The DSM desktop global (SYNO.SDS) the App window runs in; absent outside DSM. */
export interface DsmDesktop{
 AppLaunch?:(id:string,params?:{opendir:string}|{openfile:string})=>void
 StatusNotifier?:{isAppEnabled?:(id:string)=>boolean;isAppLaunchable?:(id:string)=>boolean}
}

export interface FileStationOptions{
 /** The volume mount the path is under, when known; otherwise /volumeN is stripped. */
 mount?:string
 /** Defaults to the page's SYNO.SDS. */
 desktop?:DsmDesktop
}

/** The File Station path for a NAS absolute path. #recycle, spaces and UTF-8
 * stay literal: this is a JS value, not a URL. */
export function fileStationPath(path:string,mount?:string):string{
 if(!path.startsWith('/'))return path
 const known=(mount??'').replace(/\/+$/,'')
 if(known&&(path===known||path.startsWith(`${known}/`)))return path.slice(known.length)||'/'
 const volume=/^\/volume(?:USB|SATA)?\d+(?=\/|$)/.exec(path)
 return volume?path.slice(volume[0].length)||'/':path
}

/** Opens File Station showing the folder. False when nothing was launched:
 * outside DSM, a path that is not one shared-folder path, or File Station disabled or not launchable. */
export function openFolderInFileStation(path:string,options:FileStationOptions={}):boolean{
 return launch({opendir:fileStationPath(path,options.mount)},options)
}

/** Opens File Station at the file's folder with the file selected. False as for openFolderInFileStation. */
export function openFileInFileStation(path:string,options:FileStationOptions={}):boolean{
 return launch({openfile:fileStationPath(path,options.mount)},options)
}

// The CLI's DSM window applies the same rule (host-file-navigation.ts).
function sharedFolderPath(path:string):boolean{
 if(path.length>4096||/[\x00-\x1f\x7f\\]/.test(path))return false
 const parts=path.split('/')
 return parts[0]===''&&parts.length>1&&parts.slice(1).every(part=>part!==''&&part!=='.'&&part!=='..')
}

function launch(params:{opendir:string}|{openfile:string},options:FileStationOptions):boolean{
 const desktop='desktop' in options?options.desktop:(globalThis as {SYNO?:{SDS?:DsmDesktop}}).SYNO?.SDS
 const status=desktop?.StatusNotifier
 const target='opendir' in params?params.opendir:params.openfile
 if(!sharedFolderPath(target)||typeof desktop?.AppLaunch!=='function')return false
 if(status?.isAppEnabled?.(FILE_STATION_APP)===false||status?.isAppLaunchable?.(FILE_STATION_APP)===false)return false
 desktop.AppLaunch(FILE_STATION_APP,params)
 return true
}
