import {readdirSync,readFileSync} from 'node:fs'
import {resolve} from 'node:path'
import {expect,test} from 'vitest'
import {uiLocales} from '../src/index'

// DSM 7.4's own words, copied from the templates' native-go/i18n/glossary.json without the app keys:
// the SDK's catalogs are checked here, before a release ships them.
const glossary:{terms:Array<{text:Record<string,string>}>}=JSON.parse(readFileSync(resolve(__dirname,'dsm-glossary.json'),'utf8'))
const dir=resolve(__dirname,'../i18n')
const flat=(value:unknown,prefix=''):Array<[string,string]>=>typeof value==='string'
 ?[[prefix,value]]
 :Object.entries(value as object).flatMap(([key,child])=>flat(child,prefix?`${prefix}.${key}`:key))
// One translated string: where it lives, its English source and its text in each locale.
const translations=()=>readdirSync(dir).flatMap(capability=>{
 const catalogs=Object.fromEntries(uiLocales.map(locale=>[locale,new Map(flat(JSON.parse(readFileSync(resolve(dir,capability,`${locale}.json`),'utf8'))))]))
 return [...catalogs.en.keys()].map(key=>({where:`${capability} ${key}`,en:catalogs.en.get(key)!,texts:Object.fromEntries(uiLocales.map(locale=>[locale,catalogs[locale].get(key)??'']))}))
})

test('catalogs label sizes like DSM: 1024-based KB, MB, GB and TB',()=>{
 const binary=/\d\s*(?:\{\w+\}\s*)?(?:KiB|MiB|GiB|TiB|Kio|Mio|Gio|Tio)\b|\}\s*(?:KiB|MiB|GiB|TiB|Kio|Mio|Gio|Tio)\b/
 expect(translations().flatMap(({where,texts})=>Object.entries(texts).filter(([,text])=>binary.test(text)).map(([locale,text])=>`${where} ${locale}: ${text}`))).toEqual([])
})

// \b treats accented letters as boundaries ("requêtes" would match "tes"), so words are bounded by \p{L}.
const words=(...list:string[])=>new RegExp(`(?<!\\p{L})(?:${list.join('|')})(?!\\p{L})`,'iu')
// A tú/tu imperative is also the third person ("Esta aplicación usa…"), so it only counts where a clause starts.
const clauseStart=(...list:string[])=>new RegExp(`(?:^|[.;:!?,]\\s*)(?:${list.join('|')})(?!\\p{L})`,'iu')
// DSM's register: formal address, its mainland word for busy (zh-TW 忙碌中 is zh-CN 繁忙), and its own spelling of folder in Japanese.
const OFF_GLOSSARY:Record<string,RegExp[]>={
 'zh-TW':[/您/],
 // DSM zh-CN signs out with 注销 and writes 账号/账户, never 退出登录 or 帐号/帐户.
 'zh-CN':[/您/,/忙碌/,/退出登录/,/帐号/,/帐户/],
 ja:[/フォルダー/],
 ko:[/세요/],
 de:[words('du','dich','dir','dein(?:e[mnrs]?)?')],
 fr:[words('tu','toi','ton','tes')],
 es:[
  words('tú','tu','tus','ti','contigo','has','puedes'),
  clauseStart('inicia','elige','selecciona','comprueba','revisa','vuelve','intenta','introduce','usa','abre','pide','espera','consulta','actualiza'),
 ],
 it:[words('tu','tuo','tua','tuoi','tue','ti','hai','puoi','devi','dovrai','revochi','scelga')],
}
// DSM Italian keeps buttons imperative ("Riprova") but writes instructions as infinitives ("Riprovare più tardi").
const IT_SENTENCE_IMPERATIVE=clauseStart('accedi','aggiorna','riprova','controlla','scegli','chiedi','inserisci','attendi','contatta','apri','usa','conferma','approva','seleziona','reinstalla','eliminane','importala','disconnettiti','aggiornate','accedete','riprovate','attivalo','preseleziona','confermala','annulla','elimina','concedi','modifica')
// The glossary "Back" is the wizard step; folder navigation keeps its own word.
const GLOSSARY_EXEMPT=new Set(['Back'])

test('catalogs follow the DSM glossary register and terms',()=>{
 const problems:string[]=[]
 for(const {where,en,texts} of translations()){
  const term=GLOSSARY_EXEMPT.has(en)?undefined:glossary.terms.find(t=>t.text.en===en)
  for(const locale of Object.keys(OFF_GLOSSARY)){
   const text=texts[locale]
   if(OFF_GLOSSARY[locale].some(pattern=>pattern.test(text)))problems.push(`${where} ${locale}: ${text}`)
   if(locale==='it'&&/[.!?]$/.test(text)&&IT_SENTENCE_IMPERATIVE.test(text))problems.push(`${where} ${locale}: ${text}`)
   if(term&&term.text[locale]!==text)problems.push(`${where} ${locale}: "${text}" should be glossary "${term.text[locale]}"`)
  }
 }
 expect(problems,problems.join('\n')).toEqual([])
})

// Simplified Chinese writes account as 账号 or 账户; 帐号 and 帐户 stay only where a DSM glossary string is copied word for word.
test('zh-CN catalogs write 账号 and 账户, not 帐号 or 帐户, outside DSM glossary strings',()=>{
 const dsm=new Set(glossary.terms.map(term=>term.text['zh-CN']))
 const offenders=translations().filter(({texts})=>/帐[号户]/.test(texts['zh-CN'])&&!dsm.has(texts['zh-CN'])).map(({where,texts})=>`${where}: ${texts['zh-CN']}`)
 expect(offenders).toEqual([])
})
