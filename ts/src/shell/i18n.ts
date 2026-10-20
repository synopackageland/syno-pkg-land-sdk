import en from '../../i18n/shell/en.json'
import {catalogs,uiLocale} from '../i18n'
export type ShellCopy = typeof en
export const shellCatalogs =/*#__PURE__*/catalogs(import.meta.glob('../../i18n/shell/*.json',{eager:true,import:'default'}), en)
export function shellCopy(locale: string): ShellCopy {return shellCatalogs[uiLocale(locale)]}
