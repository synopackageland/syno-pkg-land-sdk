import en from '../../i18n/bridge/en.json'
import {catalogs,uiLocale} from '../i18n'
// Generated from spec/bridge/v1/errors.json by scripts/gen-bridge-i18n.mjs.
export const bridgeCatalogs:Record<string,Record<string,string>>=/*#__PURE__*/catalogs(import.meta.glob('../../i18n/bridge/*.json',{eager:true,import:'default'}),en)
/** The text of a code the bridge runtime adds (app_unavailable, and the
 * services' limit_exceeded, read_failed, identity_unavailable,
 * credential_not_ready) or of the api's
 * request_timeout, in a DSM or BCP-47 language; undefined for any other code. */
export function bridgeErrorCopy(e:unknown,locale:string):string|undefined{
 const code=(e as {code?:unknown})?.code
 const catalog=bridgeCatalogs[uiLocale(locale)]!
 return typeof code==='string'&&Object.hasOwn(catalog,code)?catalog[code]:undefined
}
