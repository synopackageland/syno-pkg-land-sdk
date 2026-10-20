import {authorizationCopy} from './i18n'

/** Only entry-policy refusals identify an App access remedy. Other denials
 * can concern a file, an operation or the request's origin. */
export type AppDenial='app_privilege_denied'|'admin_required'
export function appDenial(error:unknown):AppDenial|undefined{
 const code=(error as {code?:unknown})?.code
 return code==='app_privilege_denied'||code==='admin_required'?code:undefined
}

/** Shared recovery copy for an explicit entry-policy refusal. No guess is
 * made from a generic permission code, HTTP status, or backend prose. */
export function appRefusal(error:unknown,locale='en',name?:string):{title:string;message:string}|undefined{
 const code=appDenial(error)
 if(!code)return
 const displayName=name?.trim()??''
 const text=authorizationCopy(locale,displayName)
 return {title:text(`${code}.title`),message:text(`${code}.${code==='app_privilege_denied'&&displayName?'namedBody':'body'}`)}
}
