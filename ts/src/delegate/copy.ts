import {appRefusal} from '../authorization/refusal'
import en from '../../i18n/delegate/en.json'
import {catalogs,uiLocale} from '../i18n'
export type DelegateCopy=typeof en
export const delegateCatalogs=/*#__PURE__*/catalogs(import.meta.glob('../../i18n/delegate/*.json',{eager:true,import:'default'}),en)
export function delegateCopy(locale='en'){return delegateCatalogs[uiLocale(locale)]}
export function errorCopy(error:unknown,copy:DelegateCopy,locale?:string,name?:string){
 const refusal=appRefusal(error,locale??Object.entries(delegateCatalogs).find(([,value])=>value===copy)?.[0],name)
 if(refusal)return refusal.message
 const code=(error as {code?:string})?.code;return ['session_required','AUTH_DENIED'].includes(code??'')?copy.session:['DENIED','SCOPE_DENIED','PERMISSION_DENIED','APP_DENIED','INELIGIBLE','denied'].includes(code??'')?copy.denied:copy.error}
