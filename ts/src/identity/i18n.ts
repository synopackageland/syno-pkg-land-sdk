import en from '../../i18n/identity/en.json'
import {catalogs,uiLocale} from '../i18n'
export type IdentityCopy=typeof en
export type IdentityNotice=''|keyof IdentityCopy['notices']
export const identityCatalogs=/*#__PURE__*/catalogs(import.meta.glob('../../i18n/identity/*.json',{eager:true,import:'default'}),en)
export function identityCopy(locale:string):IdentityCopy{return identityCatalogs[uiLocale(locale)]}
