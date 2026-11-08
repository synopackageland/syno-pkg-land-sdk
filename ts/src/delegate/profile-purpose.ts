import {catalogs,uiLocale,type UiLocale} from '../i18n'
import en from '../../i18n/webapi/en.json'

// Display metadata only. Match the exact English purpose, never a profile
// name: custom profiles may reuse names for entirely different permissions.
export const profilePurposeCatalogs=catalogs(import.meta.glob('../../i18n/webapi/*.json',{eager:true,import:'default'}),en)
const known=new Map(Object.entries(en).map(([key,value])=>[value,key as keyof typeof en]))
export function profilePurpose(profile:{purpose:string;purpose_i18n?:Partial<Record<UiLocale,string>>},locale='en'):string{
 const language=uiLocale(locale)
 if(profile.purpose_i18n)return profile.purpose_i18n[language]?.trim()||profile.purpose_i18n.en?.trim()||profile.purpose
 const key=known.get(profile.purpose)
 return key?profilePurposeCatalogs[language][key]:profile.purpose
}
