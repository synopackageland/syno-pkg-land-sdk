import {readdirSync,readFileSync} from 'node:fs'
import {resolve} from 'node:path'
import {expect,test} from 'vitest'
import {authorizationCopy,catalogs,format,mergeCatalogs,portalCatalogs,portalMessages,uiLocale,uiLocales} from '../src/index'

const dir=resolve(__dirname,'../i18n')
const files=import.meta.glob('../i18n/*/*.json',{eager:true,import:'default'})
// A plural message is one value: each locale lists the forms its language uses.
const shape=(value:unknown):unknown=>value&&typeof value==='object'&&'other' in value?'plural':value&&typeof value==='object'?Object.fromEntries(Object.entries(value).map(([k,v])=>[k,shape(v)])):typeof value

test('every capability has every locale with the keys of en',()=>{
 for(const capability of readdirSync(dir)){
  expect(readdirSync(resolve(dir,capability)).sort(),capability).toEqual(uiLocales.map(l=>`${l}.json`).sort())
  const all=catalogs(Object.fromEntries(Object.entries(files).filter(([path])=>path.startsWith(`../i18n/${capability}/`))),{})
  for(const locale of uiLocales)expect(shape(all[locale]),`${capability}/${locale}`).toEqual(shape(all.en))
  expect(all.ja).toEqual(JSON.parse(readFileSync(resolve(dir,capability,'ja.json'),'utf8')))
 }
})

test('DSM language codes and region tags resolve to a built-in locale',()=>{
 expect(['enu','cht','chs','jpn','krn','ger','fre','spn','ita'].map(uiLocale)).toEqual(['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])
 expect([uiLocale('zh_TW'),uiLocale('de-AT'),uiLocale('pt-BR')]).toEqual(['zh-TW','de','en'])
})

test('authorization copy resolves DSM language codes like every other capability',()=>{
 expect(authorizationCopy('cht','Reclaim')('title')).toBe(authorizationCopy('zh-TW','Reclaim')('title'))
 expect(authorizationCopy('cht','Reclaim')('title')).not.toBe(authorizationCopy('en','Reclaim')('title'))
})

test('format fills named values and picks the plural form for count by locale',()=>{
 expect(format('Open {name}',{name:'a.txt'})).toBe('Open a.txt')
 const files={one:'{count} file in {name}',other:'{count} files in {name}'}
 expect(format(files,{count:1,name:'x'})).toBe('1 file in x')
 expect(format(files,{count:2,name:'x'},'en')).toBe('2 files in x')
 expect(format(files,{count:0},'fr')).toBe('0 file in {name}')
 expect(format({other:'{count} 個檔案'},{count:1},'cht')).toBe('1 個檔案')
 expect(format({one:'{count} plik',few:'{count} pliki',other:'{count} plików'},{count:3},'pl')).toBe('3 pliki')
 expect(format('{missing} stays',{})).toBe('{missing} stays')
})

test('mergeCatalogs layers App catalogs over SDK ones and falls back to en per key',()=>{
 const sdk=catalogs({'../i18n/x/en.json':{a:'A',nested:{b:'B',c:'C'}},'../i18n/x/ja.json':{a:'あ',nested:{b:'ベ',c:'シ'}}},{a:'',nested:{b:'',c:''}})
 const merged=mergeCatalogs(sdk,{'../app/en.json':{nested:{c:'App C'},extra:{one:'{count} item',other:'{count} items'}},'../app/ja.json':{a:'アプリ'}})
 expect(merged.en).toEqual({a:'A',nested:{b:'B',c:'App C'},extra:{one:'{count} item',other:'{count} items'}})
 expect(merged.ja).toEqual({a:'アプリ',nested:{b:'ベ',c:'シ'},extra:{one:'{count} item',other:'{count} items'}})
 // A locale keeps its SDK translation where the App overrides only en.
 expect(merged.de).toEqual({...merged.en,nested:{b:'B',c:'C'}})
 expect(sdk.de).toEqual(sdk.en)
})

test('a locale replaces a plural message whole, without the en forms it does not use',()=>{
 const sdk=catalogs({'../i18n/x/en.json':{n:{one:'{count} item',other:'{count} items'}},'../i18n/x/ja.json':{n:{other:'{count} 件'}}},{n:{one:'',other:''}})
 expect(sdk.ja.n).toEqual({other:'{count} 件'})
 expect(format(sdk.ja.n,{count:1},'ja')).toBe('1 件')
 const merged=mergeCatalogs(sdk,{'../app/en.json':{n:{other:'{count} things'}}})
 expect(merged.en.n).toEqual({other:'{count} things'})
 expect(merged.ja.n).toEqual({other:'{count} 件'})
})

test('portal messages are the panel text in a DSM language, keyed as PortalSettings reads them',()=>{
 expect(portalMessages('cht').askAdmin).toBe('請管理員設定')
 expect(portalMessages('pt-BR')).toEqual(portalMessages('en'))
 expect(Object.keys(portalMessages('en'))).toEqual(Object.keys(portalCatalogs.en).map(key=>key.slice('portal.'.length)))
})
