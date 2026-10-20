// Every catalog lives in ../i18n/<capability>/<locale>.json; en types the others.
export type UiLocale='en'|'zh-TW'|'zh-CN'|'ja'|'ko'|'de'|'fr'|'es'|'it'
export const uiLocales:readonly UiLocale[]=['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']
const aliases:Record<string,UiLocale>={enu:'en',cht:'zh-TW',chs:'zh-CN',jpn:'ja',krn:'ko',ger:'de',fre:'fr',spn:'es',ita:'it'}

/** The built-in UI locale (shared by these catalogs and the UI's own text) for a DSM or BCP-47 language tag. */
export function uiLocale(locale:string):UiLocale{
 const normalized=locale.replaceAll('_','-')
 return [aliases[normalized]??normalized,normalized.split('-')[0]!].find((key):key is UiLocale=>uiLocales.includes(key as UiLocale))??'en'
}

/** The catalogs of one capability by locale, typed by its en catalog. Each
 * capability globs its own folder, so a bundle carries only what it imports. */
export function catalogs<T>(files:Record<string,unknown>,_en:T):Record<UiLocale,T>{
 const byLocale=byFileLocale(files)
 return Object.fromEntries(uiLocales.map(locale=>[locale,merge(byLocale.en,byLocale[locale]) as T])) as Record<UiLocale,T>
}

/** SDK catalogs with an App's catalogs (files by path, as import.meta.glob
 * returns them) layered over them key by key. A key missing from a locale
 * falls back to the merged en catalog. */
export function mergeCatalogs<T extends object>(base:Record<UiLocale,T>,files:Record<string,unknown>):Record<UiLocale,T&Record<string,unknown>>{
 const app=byFileLocale(files)
 const en=merge(base.en,app.en)
 return Object.fromEntries(uiLocales.map(locale=>[locale,merge(merge(en,base[locale]),app[locale])])) as Record<UiLocale,T&Record<string,unknown>>
}

/** A message, or its plural forms selected by the count value (Intl plural
 * categories; other is required). */
export type Message=string|{zero?:string;one?:string;two?:string;few?:string;many?:string;other:string}

/** Fills {key} placeholders from values; a placeholder without a value stays
 * visible. Plural forms are chosen by values.count in the given locale. */
export function format(message:Message,values:Record<string,string|number>,locale='en'):string{
 const text=typeof message==='string'?message:message[pluralCategory(values.count,locale)]??message.other
 return text.replace(/\{(\w+)\}/g,(match,key:string)=>key in values?String(values[key]):match)
}

function pluralCategory(count:string|number|undefined,locale:string):Intl.LDMLPluralRule{
 return new Intl.PluralRules(aliases[locale]??locale.replaceAll('_','-')).select(Number(count??0))
}

function byFileLocale(files:Record<string,unknown>):Record<string,unknown>{
 return Object.fromEntries(Object.entries(files).map(([path,catalog])=>[path.slice(path.lastIndexOf('/')+1,-'.json'.length),catalog]))
}

const isObject=(value:unknown):value is Record<string,unknown>=>typeof value==='object'&&value!==null&&!Array.isArray(value)

// A plural message is a leaf: a locale lists only the forms its language uses.
const isPlural=(value:Record<string,unknown>)=>typeof value.other==='string'

function merge(base:unknown,over:unknown):unknown{
 if(!isObject(base)||!isObject(over)||isPlural(over))return over??base
 const out:Record<string,unknown>={...base}
 for(const [key,value] of Object.entries(over))out[key]=merge(base[key],value)
 return out
}

/** The colon between a label and its value: full-width in Chinese and Japanese. */
export function labelSeparator(locale:string):string{return /^(zh|ja)/.test(uiLocale(locale))?'：':': '}
