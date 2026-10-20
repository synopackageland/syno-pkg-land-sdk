import {protocolCodes,type ProtocolOp,type ProtocolParams,type RefusalCode} from './v1'

/** remote_addr: the browser's address (REMOTE_ADDR); the executor checks App Privileges for it and refuses without it. */
export interface ProtocolAuth {dsm_cookie:string;dsm_synotoken:string;remote_addr?:string}
export type ProtocolResponse={ok:true;result:Record<string,unknown>}|{ok:false;code:RefusalCode;message:string}
const maxRequestLine=64*1024
const object=(value:unknown):value is Record<string,unknown>=>!!value&&typeof value==='object'&&!Array.isArray(value)
function protocol():never{throw {code:'EXECUTOR_PROTOCOL'}}

/** The request line for the executor socket. A present auth is always sent, even when empty. */
export function encodeRequest<Op extends ProtocolOp>(op:Op,params:ProtocolParams[Op],auth?:ProtocolAuth):string{
 const line=JSON.stringify({v:1,op,...(auth?{auth}:{}),params})+'\n'
 if(new TextEncoder().encode(line).length>maxRequestLine)throw {code:'REQUEST_TOO_LARGE'}
 return line
}

/** Parse one response line. Match refusals on code, never on message. */
export function decodeResponse(line:string):ProtocolResponse{
 let value:unknown
 try{value=JSON.parse(line)}catch{protocol()}
 if(!object(value)||typeof value.ok!=='boolean')protocol()
 if(value.ok){
  if(!object(value.result))protocol()
  return {ok:true,result:value.result}
 }
 const code=value.code as keyof typeof protocolCodes
 if(protocolCodes[code]?.kind!=='refusal'||typeof value.message!=='string')protocol()
 return {ok:false,code:code as RefusalCode,message:value.message}
}
