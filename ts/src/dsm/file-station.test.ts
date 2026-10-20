import {expect,test} from 'vitest'
import {FILE_STATION_APP,fileStationPath,openFileInFileStation,openFolderInFileStation,type DsmDesktop} from '../index'

function desktop(status?:{enabled:boolean;launchable:boolean}){
 const calls:unknown[][]=[]
 const sds:DsmDesktop={AppLaunch:(...args)=>{calls.push(args)}}
 if(status)sds.StatusNotifier={isAppEnabled:()=>status.enabled,isAppLaunchable:()=>status.launchable}
 return {sds,calls}
}

test('File Station paths are shared-folder paths without the volume mount',()=>{
 expect(fileStationPath('/volume1/photo/2026')).toBe('/photo/2026')
 expect(fileStationPath('/volume12/photo')).toBe('/photo')
 expect(fileStationPath('/volumeUSB1/usbshare1/a','/volumeUSB1/usbshare1')).toBe('/a')
 expect(fileStationPath('/photo/#recycle/a b')).toBe('/photo/#recycle/a b')
 expect(fileStationPath('/volume1')).toBe('/')
})

test('folders open with opendir and files are selected with openfile',()=>{
 const {sds,calls}=desktop()
 expect(openFolderInFileStation('/volume1/photo/2026',{desktop:sds})).toBe(true)
 expect(openFileInFileStation('/volume1/photo/a.jpg',{desktop:sds})).toBe(true)
 expect(calls).toEqual([[FILE_STATION_APP,{opendir:'/photo/2026'}],[FILE_STATION_APP,{openfile:'/photo/a.jpg'}]])
})

test('nothing launches outside DSM, for an empty path or when File Station is not available',()=>{
 expect(openFolderInFileStation('/photo',{desktop:undefined})).toBe(false)
 for(const status of [{enabled:false,launchable:true},{enabled:true,launchable:false}]){
  const {sds,calls}=desktop(status)
  expect(openFolderInFileStation('/photo',{desktop:sds})).toBe(false)
  expect(calls).toEqual([])
 }
 const {sds,calls}=desktop({enabled:true,launchable:true})
 expect(openFolderInFileStation('',{desktop:sds})).toBe(false)
 expect(openFolderInFileStation('/photo',{desktop:sds})).toBe(true)
 expect(calls.length).toBe(1)
})

test('the default desktop is the DSM page global',()=>{
 const {sds,calls}=desktop()
 ;(globalThis as {SYNO?:unknown}).SYNO={SDS:sds}
 try{expect(openFolderInFileStation('/volume1/photo')).toBe(true)}finally{delete (globalThis as {SYNO?:unknown}).SYNO}
 expect(calls).toEqual([[FILE_STATION_APP,{opendir:'/photo'}]])
})

test('only DSM volume mounts are stripped, never a shared folder named like one',()=>{
 expect(fileStationPath('/volumeSATA1/esata/a')).toBe('/esata/a')
 expect(fileStationPath('/volumes2/a')).toBe('/volumes2/a')
 expect(fileStationPath('/volume-old1/a')).toBe('/volume-old1/a')
})

test('a path that is not one canonical shared-folder path never launches',()=>{
 const {sds,calls}=desktop()
 for(const path of ['/','relative/file','/volume1','/volume1/','/photo/../secret','/volume1/../volume2/photo','/photo/./a','/photo//a','/photo/a/','/photo/a\\b','/photo/a\0b','/photo/a\nb','/photo/'+'a'.repeat(4096)]){
  expect(openFolderInFileStation(path,{desktop:sds}),JSON.stringify(path)).toBe(false)
  expect(openFileInFileStation(path,{desktop:sds}),JSON.stringify(path)).toBe(false)
 }
 expect(calls).toEqual([])
})
