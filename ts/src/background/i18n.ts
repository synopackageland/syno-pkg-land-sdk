import {appRefusal} from '../authorization/refusal'
import en from '../../i18n/background/en.json'
import {catalogs,uiLocale} from '../i18n'
import {lifecycleRefusal} from '../delegate/lifecycle'
export type BackgroundCopy=typeof en
export const backgroundCatalogs=/*#__PURE__*/catalogs(import.meta.glob('../../i18n/background/*.json',{eager:true,import:'default'}),en)
export function backgroundCopy(locale:string):BackgroundCopy{return backgroundCatalogs[uiLocale(locale)]}
// With locale, a stopped or starting package's refusal says to try again.
export function backgroundErrorCopy(e:unknown,copy:BackgroundCopy,locale?:string,name?:string):string{
 const refusal=appRefusal(e,locale??Object.entries(backgroundCatalogs).find(([,value])=>value===copy)?.[0],name)
 if(refusal)return refusal.message
 const lifecycle=lifecycleRefusal(e,locale)
 if(lifecycle)return lifecycle
 const code=(e as {code?:string})?.code??''
 return (copy.errors as Record<string,string>)[code]??copy.errors.failed
}
