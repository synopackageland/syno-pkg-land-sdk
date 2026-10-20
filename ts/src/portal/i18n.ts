import en from '../../i18n/portal/en.json'
import {catalogs,uiLocale} from '../i18n'
export const portalCatalogs:Record<string,Record<string,string>>=/*#__PURE__*/catalogs(import.meta.glob('../../i18n/portal/*.json',{eager:true,import:'default'}),en)
/** PortalAccess.messages: the panel text in a DSM or BCP-47 language, keyed without the portal. prefix. */
export function portalMessages(locale:string):Record<string,string>{
 return Object.fromEntries(Object.entries(portalCatalogs[uiLocale(locale)]!).map(([key,text])=>[key.slice('portal.'.length),text]))
}
