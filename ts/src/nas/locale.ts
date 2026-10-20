import {appRefusal} from '../authorization/refusal'
import en from '../../i18n/files/en.json'
import {catalogs,uiLocale} from '../i18n'
import {lifecycleRefusal} from '../delegate/lifecycle'
export type NasCopy=typeof en
export const nasCatalogs=/*#__PURE__*/catalogs(import.meta.glob('../../i18n/files/*.json',{eager:true,import:'default'}),en)
export function nasCopy(locale:string):NasCopy{return nasCatalogs[uiLocale(locale)]}
// With locale, a stopped or starting package's refusal says to try again.
export function nasErrorCopy(error:unknown,copy:NasCopy,locale?:string,name?:string):string{
 const refusal=appRefusal(error,locale??Object.entries(nasCatalogs).find(([,value])=>value===copy)?.[0],name)
 if(refusal)return refusal.message
 const lifecycle=lifecycleRefusal(error,locale)
 if(lifecycle)return lifecycle
 const code=(error as {code?:string})?.code
 const keys:Record<string,keyof NasCopy>={session_required:'errorSession',dsm_session_required:'errorDSM',upload_size_required:'errorUploadSize',denied:'errorDenied',not_found:'errorNotFound',conflict:'errorConflict','commit-uncertain':'errorUncertain','invalid-request':'errorInvalid',canceled:'errorCanceled',timeout:'errorTimeout','origin-refused':'errorOrigin'}
 return copy[keys[code??'']??'errorUnavailable']
}
