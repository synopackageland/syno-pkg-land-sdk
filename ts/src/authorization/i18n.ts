import en from '../../i18n/authorization/en.json'
import {catalogs,uiLocale} from '../i18n'
export const authorizationCatalogs:Record<string,Record<string,string>>=/*#__PURE__*/catalogs(import.meta.glob('../../i18n/authorization/*.json',{eager:true,import:'default'}),en)
/** Root-task semantics are owned by the native product. */
export function authorizationCopy(locale:string,packageName:string){
 const catalog={...en,...authorizationCatalogs[uiLocale(locale)]}
 return (key:string)=>((catalog as Record<string,string>)[`authorization.${key}`]||key).replaceAll('{package}',packageName)
}
