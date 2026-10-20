import type {NasProvider} from '../nas/contract'
/** A UX bound only; the executor independently enforces its protected scope. */
export function scopedPicker(source:NasProvider,root:string):NasProvider{
 const check=(path:string)=>{
  if(!root||!(path===root||path.startsWith(root+'/'))||path.includes('\\')||path.slice(1).split('/').some(p=>!p||p==='.'||p==='..'))throw {code:'SCOPE_DENIED'}
 }
 return {
  async shares(offset,signal){
   check(root)
   if(offset!==0)return {entries:[],offset,total:1}
   const entry=await source.info(root,signal)
   if(entry.path!==root||!entry.isDir)throw {code:'SCOPE_DENIED'}
   return {entries:[entry],offset:0,total:1}
  },
  async list(path,options,signal){check(path);const page=await source.list(path,options,signal);page.entries.forEach(e=>check(e.path));return page},
  async info(path,signal){check(path);const entry=await source.info(path,signal);check(entry.path);return entry},
  async createFolder(parent,name,signal){check(parent);check(parent+'/'+name);return source.createFolder(parent,name,signal)},
  // The parent owns the shared session provider; closing a picker must not dispose it.
  dispose(){},
 }
}
