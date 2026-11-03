// Browser fixture for scripts/picker-browser.mjs: the NAS picker over an
// in-memory provider whose requests wait while window.picker.hold is true.
import '@synopackageland/ui/themes/dsm7-embedded.css'
import {createApp,h,ref} from 'vue'
import {AppRoot,ConfigProvider,type UiLocale} from '@synopackageland/ui'
import NasPicker from '../../src/nas/NasPicker.vue'
import type {NasEntry,NasProvider} from '../../src/nas/contract'

const dir=(path:string):NasEntry=>({name:path.split('/').pop()!,path,isDir:true})
const file=(path:string,bytes:number):NasEntry=>({name:path.split('/').pop()!,path,isDir:false,bytes})
const long='quarterly-planning-material-for-the-regional-distribution-team-2026-final-review'
const tree:Record<string,NasEntry[]>={
 '':[dir('/projects'),dir('/media'),dir(`/${long}`)],
 '/projects':[dir('/projects/design'),dir('/projects/reports'),dir(`/projects/${long}`),file('/projects/notes.txt',2048),file(`/projects/${long}.pdf`,73400320)],
 '/projects/design':[],
 '/projects/reports':[dir('/projects/reports/2025'),dir('/projects/reports/2026')],
 [`/projects/${long}`]:[dir(`/projects/${long}/${long}`)],
 [`/projects/${long}/${long}`]:[],
 '/projects/reports/2025':[],'/projects/reports/2026':[],
 '/media':[],[`/${long}`]:[],
}
const params=new URLSearchParams(location.search)
const state={hold:params.has('hold'),waiting:[] as Array<()=>void>,calls:[] as string[]}
function wait(){return state.hold?new Promise<void>(r=>state.waiting.push(r)):Promise.resolve()}
async function page(key:string,offset:number){
 state.calls.push(key);await wait()
 const entries=tree[key]??[]
 return {entries:entries.slice(offset),total:entries.length,offset}
}
const provider:NasProvider={
 shares:(offset)=>page('',offset),
 list:(path,options)=>page(path,options.offset??0),
 info:async path=>dir(path),
 createFolder:async()=>{},
 dispose(){},
}
const open=ref(true),locale=(params.get('locale')??'zh-TW') as UiLocale
const destination=params.has('plain')?undefined:{key:'save',selectDestination:async({key,path}:{key:string;path:string})=>({id:'g1',key,path,username:'user',lifetime:'until-revoked' as const})}
;(window as any).picker={
 get pending(){return state.waiting.length},
 get calls(){return state.calls},
 hold(on:boolean){state.hold=on;if(!on)state.waiting.splice(0).forEach(r=>r())},
 open(){open.value=true},
}
const app=createApp({render:()=>h(ConfigProvider,{locale},()=>h(AppRoot,{},()=>h(NasPicker,{provider,locale,mode:'folder',open:open.value,destination,'onUpdate:open':(v:boolean)=>{open.value=v}})))})
app.config.idPrefix='picker-fixture'
app.mount('#app')
