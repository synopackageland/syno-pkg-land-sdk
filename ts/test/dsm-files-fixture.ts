import {vi} from 'vitest'

// UI fixtures speak DSM's browser HTTP envelope. The App admission call is
// separate, so a UI test cannot accidentally restore a production CGI relay.
export function nasContext(read:(method:string,params?:any)=>Promise<any>){
 vi.stubGlobal('fetch',vi.fn(async(url:string,init?:RequestInit)=>{
  if(url!=='/webapi/entry.cgi')throw Error('unexpected DSM endpoint')
  const body=init!.body as URLSearchParams,method=body.get('method')
  let result:unknown
  if(method==='list_share')result=await read('nas.files.shares',{offset:Number(body.get('offset'))})
  else if(method==='list')result=await read('nas.files.list',{path:body.get('folder_path'),offset:Number(body.get('offset')),limit:Number(body.get('limit')),sortBy:body.get('sort_by'),sortDirection:body.get('sort_direction')})
  else if(method==='getinfo')result={files:[await read('nas.files.info',{path:JSON.parse(body.get('path')!)[0]})]}
  else if(method==='create')result=await read('nas.files.create_folder',{parent:JSON.parse(body.get('folder_path')!)[0],name:JSON.parse(body.get('name')!)[0]})
  else throw Error('unexpected DSM operation')
  return new Response(JSON.stringify({success:true,data:result}))
 }))
 return {call:async<T,>()=>({mode:'browser-session'}) as T,files:{token:()=> 'fixture-token'}}
}

export function scopeShares(shares:{name:string;path:string;isdir:boolean}[]){
 vi.stubGlobal('SYNO',{SDS:{Session:{SynoToken:'fixture-token'}}})
 vi.stubGlobal('fetch',vi.fn(async(url:string,init?:RequestInit)=>{
  if(url!=='/webapi/entry.cgi'||(init?.body as URLSearchParams).get('method')!=='list_share')throw Error('unexpected scope request')
  return new Response(JSON.stringify({success:true,data:{shares,total:shares.length,offset:0}}))
 }))
}
