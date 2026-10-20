#!/usr/bin/env node
// Generate i18n/bridge/<locale>.json from spec/bridge/v1/errors.json: each
// code the bridge runtime adds, with its text, keyed by code.
// Usage: node scripts/gen-bridge-i18n.mjs [errors.json] [out dir]
import {mkdirSync,readFileSync,writeFileSync} from 'node:fs'
import {dirname,resolve} from 'node:path'
import {fileURLToPath} from 'node:url'

const here=dirname(fileURLToPath(import.meta.url))
const locales=['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']

/** Each locale's catalog for the errors.json file, as JSON text. */
export function generate(errors){
 const codes=JSON.parse(readFileSync(errors,'utf8')).codes
 return Object.fromEntries(locales.map(locale=>{
  const catalog=Object.fromEntries(codes.map(c=>{
   if(typeof c[locale]!=='string')throw Error(`${c.code}: no ${locale} text`)
   return [c.code,c[locale]]
  }))
  return [locale,JSON.stringify(catalog,null,2)+'\n']
 }))
}

if(process.argv[1]===fileURLToPath(import.meta.url)){
 const out=resolve(process.argv[3]??resolve(here,'../i18n/bridge'))
 mkdirSync(out,{recursive:true})
 for(const [locale,text] of Object.entries(generate(resolve(process.argv[2]??resolve(here,'../../spec/bridge/v1/errors.json')))))writeFileSync(resolve(out,`${locale}.json`),text)
}
