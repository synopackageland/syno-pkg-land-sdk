import {createDsmFileStation,DSM_FILE_ENDPOINT,filePath} from '../dsm/filestation'
import {canSaveNativeURL,saveUrl} from '../mobile'
export interface FileTransport {
 /** Deprecated: ignored. File Station always uses the fixed DSM same-origin endpoint. */
 endpoint?:string
 token?:()=>string|undefined
}
export function createFileTransfers(transport:FileTransport={}){
 const dsm=createDsmFileStation(transport)
 const frames=new Set<HTMLIFrameElement>()
 const uploads=new Set<AbortController>()
 let disposed=false
 return {
  async download(path:string):Promise<void>{
   filePath(path)
   if(disposed)throw {code:'canceled'}
   const token=await dsm.token()
   if(disposed)throw {code:'canceled'}
   if(canSaveNativeURL()){
    const params=new URLSearchParams({api:'SYNO.FileStation.Download',method:'download',version:'2',path:JSON.stringify([path]),mode:'download',SynoToken:token})
    await saveUrl(`${location.origin}${DSM_FILE_ENDPOINT}?${params}`,{filename:path.split('/').pop()!})
    return
   }
   const frame=document.createElement('iframe')
   frame.hidden=true;frame.setAttribute('sandbox','allow-forms allow-same-origin allow-downloads')
   frames.add(frame);document.body.append(frame)
   const doc=frame.contentDocument
   if(!doc){frame.remove();frames.delete(frame);throw {code:'unavailable'}}
   // The isolated form has its own CSP: a DSM redirect cannot submit the
   // token to another origin. No file buffering or blob-size limit is added.
   // DSM refuses a SynoToken in a POST body (119; 7.1.1 and 7.4.1 alike) and a form cannot set
   // X-SYNO-TOKEN, so the token is in the same-origin action's query, as in
   // DSM's own media URLs.
   const csp=doc.createElement('meta');csp.httpEquiv='Content-Security-Policy';csp.content="default-src 'none'; form-action 'self'; base-uri 'none'";doc.head.append(csp)
   const form=doc.createElement('form');form.method='POST';form.action=`${location.origin}${DSM_FILE_ENDPOINT}?${new URLSearchParams({SynoToken:token})}`
   for(const [name,value] of Object.entries({api:'SYNO.FileStation.Download',method:'download',version:'2',path:JSON.stringify([path]),mode:'download'})){
    const input=doc.createElement('input');input.type='hidden';input.name=name;input.value=value;form.append(input)
   }
   doc.body.append(form)
   try{form.submit()}finally{for(const input of Array.from(form.querySelectorAll('input')))input.value='';form.action='';form.remove()}
  },
  dispose(){disposed=true;for(const frame of frames)frame.remove();frames.clear();for(const upload of uploads)upload.abort();uploads.clear()},
  async upload(parent:string,file:File,signal:AbortSignal):Promise<void>{
   if(disposed)throw {code:'canceled'}
   const controller=new AbortController(),abort=()=>controller.abort()
   uploads.add(controller);signal.addEventListener('abort',abort,{once:true})
   if(signal.aborted)controller.abort()
   try{await dsm.upload(parent,file,controller.signal)}finally{uploads.delete(controller);signal.removeEventListener('abort',abort)}
  },
 }
}
