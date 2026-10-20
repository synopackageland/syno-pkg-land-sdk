import {appDenial} from './refusal'

/** Existing authorization-check classification, including opaque HTTP refusals. */
export function isAuthorizationDenied(error:unknown):boolean{
 const {code,message}=(error??{}) as {code?:unknown;message?:unknown}
 return !!appDenial(error)||typeof code==='string'&&['APP_DENIED','PERMISSION_DENIED','cross_origin','origin_required'].includes(code)||code==='bad_response'&&/HTTP 40[13]\b/.test(String(message))
}
