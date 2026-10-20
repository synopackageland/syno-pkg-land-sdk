import en from '../../i18n/sharedfolder/en.json'
import {catalogs,uiLocale} from '../i18n'
export const sharedFolderCatalogs:Record<string,Record<string,string>>=/*#__PURE__*/catalogs(import.meta.glob('../../i18n/sharedfolder/*.json',{eager:true,import:'default'}),en)
/** SharedFolderAccess.messages: the panel text in a DSM or BCP-47 language, keyed without the sharedfolder. prefix. */
export function sharedFolderMessages(locale:string):Record<string,string>{
 return Object.fromEntries(Object.entries(sharedFolderCatalogs[uiLocale(locale)]!).map(([key,text])=>[key.slice('sharedfolder.'.length),text]))
}
