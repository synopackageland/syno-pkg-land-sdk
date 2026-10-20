import {appRefusal} from '../authorization/refusal'
// Background-save copy; DelegatePanel's copy is in ./copy.
import en from '../../i18n/save/en.json'
import {catalogs,uiLocale} from '../i18n'
import {lifecycleRefusal} from './lifecycle'
export const saveCatalogs=/*#__PURE__*/catalogs(import.meta.glob('../../i18n/save/*.json',{eager:true,import:'default'}),en)
export function saveCopy(locale='en'){return saveCatalogs[uiLocale(locale)]}
// A refused folder choice ('select') created no grant; a refused save kept it.
// With locale, a stopped or starting package's refusal says to try again.
export function saveError(error:unknown,copy=saveCopy(),situation:'save'|'select'='save',locale?:string,name?:string){
 const refusal=appRefusal(error,locale??Object.entries(saveCatalogs).find(([,value])=>value===copy)?.[0],name)
 if(refusal)return refusal.message
 const select=situation==='select'
 const code=(error as {code?:string})?.code
 const lifecycle=lifecycleRefusal(error,locale)
 if(lifecycle)return lifecycle
 if(code==='disabled')return copy.disabled
 if(['EXECUTOR_UNAVAILABLE','needs-admin-approval','needs-scope-confirmation'].includes(code??''))return copy.needs
 if(['session_required','dsm_session_required','AUTH_DENIED'].includes(code??''))return copy.session
 if(['DENIED','DELEGATION_DENIED','IDENTITY_CHANGED','SCOPE_DENIED','INELIGIBLE','DESTINATION_CHANGED','denied','grant-revoked','identity-changed','permission-changed'].includes(code??''))return copy.denied
 // These keep the grant: fix the cause, then save again without choosing anew.
 if(code==='PERMISSION_DENIED')return select?copy.permissionSelect:copy.permission
 if(code==='not-found')return copy.missing
 if(code==='APP_DENIED')return select?copy.appPrivilegeSelect:copy.appPrivilege
 if(['CONFLICT','conflict','file-exists','destination-exists'].includes(code??''))return copy.conflict
 if(['NO_SPACE','ENOSPC','no-space'].includes(code??''))return copy.space
 if(['uncertain','commit-uncertain'].includes(code??''))return copy.uncertainHelp
 return copy.error
}
