import {readdirSync,readFileSync} from 'node:fs'
import {join} from 'node:path'
import {expect,test} from 'vitest'
// A Chinese or Japanese label takes a full-width colon ("範圍：/x", not "範圍: /x").
test('Chinese and Japanese catalogs use a full-width colon after a label',()=>{
 const root=join(__dirname,'..','i18n'),problems:string[]=[]
 for(const capability of readdirSync(root))for(const locale of ['zh-TW','zh-CN','ja']){
  let catalog:Record<string,unknown>
  try{catalog=JSON.parse(readFileSync(join(root,capability,locale+'.json'),'utf8'))}catch{continue}
  for(const [key,value] of Object.entries(catalog))for(const text of typeof value==='string'?[value]:Object.values(value as object))
   if(/[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}]: ?\S/u.test(String(text)))problems.push(`${capability}/${locale} ${key}: ${text}`)
 }
 expect(problems,problems.join('\n')).toEqual([])
})
