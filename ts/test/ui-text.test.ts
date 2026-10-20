import {readFileSync,readdirSync} from 'node:fs'
import {relative,resolve} from 'node:path'
import {expect,test} from 'vitest'
import {identityCatalogs,uiLocales} from '../src/index'

// Attributes whose static value is shown or announced to the user.
const VISIBLE_ATTRIBUTES=/(?<![:\w-])(title|label|description|placeholder|aria-label|loading-text|close-label|alt)="([^"]*)"/g
// A capitalized word followed by more words reads as an English sentence.
const PROSE=/^[A-Z][A-Za-z]*(?:[ ’'][^\s]+)+/
const src=resolve(__dirname,'../src')

const sources=(dir=src):string[]=>readdirSync(dir,{withFileTypes:true}).flatMap(entry=>{
 const path=resolve(dir,entry.name)
 if(entry.isDirectory())return entry.name==='protocol'?[]:sources(path)
 return /\.vue$|(?<!\.test)\.ts$/.test(entry.name)?[path]:[]
})
const withoutComments=(code:string)=>code.replace(/\/\*[\s\S]*?\*\//g,'').replace(/(^|[\s;{}])\/\/.*$/gm,'$1')
const literals=(code:string)=>[...code.matchAll(/'((?:[^'\\\n]|\\.)*)'|"((?:[^"\\\n]|\\.)*)"|`((?:[^`\\]|\\.)*)`/g)].map(m=>m[1]??m[2]??m[3]??'')
function block(source:string,tag:string){
 const open=source.indexOf(`<${tag}`),close=source.lastIndexOf(`</${tag}>`)
 return open<0||close<0?'':source.slice(source.indexOf('>',open)+1,close)
}

/** User-visible English that bypasses the translation catalogs. */
function hardcoded(file:string):string[]{
 const source=readFileSync(file,'utf8')
 const scripts=file.endsWith('.vue')?[...source.matchAll(/<script[^>]*>([\s\S]*?)<\/script>/g)].map(m=>m[1]).join('\n'):source
 const found=literals(withoutComments(scripts)).filter(text=>PROSE.test(text))
 if(!file.endsWith('.vue'))return found
 const markup=block(source,'template').replace(/<!--[\s\S]*?-->/g,'')
 for(const [,,value] of markup.matchAll(VISIBLE_ATTRIBUTES))if(/[A-Za-z]/.test(value))found.push(value)
 const expressions=[...markup.matchAll(/\{\{([\s\S]*?)\}\}|\s(?::|v-bind:)[\w.-]+="([^"]*)"/g)].map(m=>m[1]??m[2])
 for(const expression of expressions)found.push(...literals(expression).filter(text=>/[A-Z]|[a-z] [a-z]/.test(text)))
 const text=markup.replace(/\{\{[\s\S]*?\}\}/g,' ').replace(/<(?:"[^"]*"|'[^']*'|[^'">])*>/g,'\n')
 found.push(...text.split('\n').map(line=>line.trim()).filter(line=>/[A-Za-z]/.test(line)))
 return found
}

test('component text goes through the translation catalogs',()=>{
 expect(sources().flatMap(file=>hardcoded(file).map(text=>`${relative(src,file)}: ${text}`))).toEqual([])
})

test('help text quotes the exact labels it points users to',()=>{
 const identity=identityCatalogs
 const problems:string[]=[]
 for(const locale of uiLocales){
  const refs:Array<[string,string,string]>=[
   ['identity.notices.revalidate',identity[locale].notices.revalidate,identity[locale].continueDSM],
  ]
  for(const [key,text,label] of refs)if(!text.includes(label))problems.push(`${locale} ${key}: missing "${label}"`)
 }
 expect(problems).toEqual([])
})

// Every string of the i18n catalogs with its capability/locale file.
const catalogTexts=(locale?:string)=>{
 const dir=resolve(__dirname,'../i18n')
 const flat=(value:unknown):string[]=>typeof value==='string'?[value]:Object.values(value as object).flatMap(flat)
 return readdirSync(dir).flatMap(capability=>readdirSync(resolve(dir,capability)).filter(file=>!locale||file===`${locale}.json`).flatMap(file=>flat(JSON.parse(readFileSync(resolve(dir,capability,file),'utf8'))).map(text=>({file:`${capability}/${file}`,text}))))
}

// DSM lays facts out as rows, columns or badges and writes a menu path with
// ">": SDK text strings nothing together with a middle dot, a dash or an arrow.
test('SDK text strings nothing together with middle dots, dashes or arrows',()=>{
 const catalogs=catalogTexts().map(({file,text})=>`${file}: ${text}`)
 const templates=sources().filter(file=>file.endsWith('.vue')).flatMap(file=>block(readFileSync(file,'utf8'),'template').split('\n').map(line=>`${relative(src,file)}: ${line.trim()}`))
 expect([...catalogs.filter(text=>/·| [—–] |→/.test(text)),...templates.filter(line=>/·/.test(line))]).toEqual([])
})

// DSM messages say what happened and what to do in a line or two; detail that
// does not change the next step stays off the screen. English sets the length
// the other locales follow.
test('English SDK text stays within 160 characters',()=>{
 expect(catalogTexts('en').filter(({text})=>text.length>160).map(({file,text})=>`${file}: ${text}`)).toEqual([])
})
