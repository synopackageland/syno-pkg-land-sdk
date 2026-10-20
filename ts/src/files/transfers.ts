export interface FileTransport {endpoint:string;token?:()=>string|undefined}
export function createFileTransfers(transport:FileTransport){
 const url=(method:string,params:Record<string,string>)=>`${transport.endpoint}?${new URLSearchParams({method,...params})}`
 const frames=new Set<HTMLIFrameElement>()
 return {
  download(path:string):void{
   const frame=document.createElement('iframe'),form=document.createElement('form'),input=document.createElement('input')
   frame.name=`spl-download-${crypto.getRandomValues(new Uint32Array(4)).join('-')}`;frame.hidden=true
   frames.add(frame);document.body.append(frame)
   form.method='POST';form.action=url('nas.files.download',{path});form.target=frame.name;form.hidden=true
   input.type='hidden';input.name='SynoToken';input.value=transport.token?.()??'';form.append(input);document.body.append(form)
   try{form.submit()}finally{input.value='';form.remove()}
  },
  dispose(){for(const frame of frames)frame.remove();frames.clear()},
  async upload(parent:string,file:File,signal:AbortSignal):Promise<void>{
   if(file.size>16*1024**3)throw {code:'upload_size_required'}
   const headers:Record<string,string>={'Content-Type':'application/octet-stream'},token=transport.token?.()
   if(token)headers['X-SYNO-TOKEN']=token
   let body:any
   try{const response=await fetch(url('nas.files.upload',{parent,name:file.name}),{method:'POST',credentials:'same-origin',headers,body:file,signal});body=await response.json()}
   catch{throw {code:'commit-uncertain'}}
   if(body?.success!==true)throw {code:typeof body?.error?.code==='string'?body.error.code:'commit-uncertain'}
  },
 }
}
