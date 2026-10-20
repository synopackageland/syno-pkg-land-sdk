import {readdirSync,readFileSync} from 'node:fs'
import {join,relative} from 'node:path'
import {expect,test} from 'vitest'
// SDK components draw colour and type from the UI's tokens (its
// docs/color-system.md), so they follow the theme and an App's accent.
const src=join(__dirname,'..','src')
const vue=(dir:string):string[]=>readdirSync(dir,{withFileTypes:true}).flatMap(e=>e.isDirectory()?vue(join(dir,e.name)):e.name.endsWith('.vue')?[join(dir,e.name)]:[])
const styles=vue(src).map(file=>({file:relative(src,file),css:[...readFileSync(file,'utf8').matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map(m=>m[1]).join('\n').replace(/\/\*[\s\S]*?\*\//g,'')}))
const declarations=(property:string)=>styles.flatMap(({file,css})=>[...css.matchAll(new RegExp(`(?<![\\w-])${property}\\s*:\\s*([^;}]+)`,'g'))].map(m=>`${file}: ${m[1].trim()}`))
// Custom properties of the installed UI theme. A token newer than the oldest
// UI the peer range allows carries a fallback, so it is not looked up here.
const theme=readFileSync(join(__dirname,'../node_modules/@synopackageland/ui/dist/theme.css'),'utf8')
const defined=new Set([...theme.matchAll(/(--ui-[\w-]+)\s*:/g)].map(m=>m[1]))

test('SDK styles take colours from the UI tokens',()=>{
 expect(styles.flatMap(({file,css})=>[...css.matchAll(/#[0-9a-f]{3,8}\b|\b(?:rgba?|hsla?)\(/gi)].map(m=>`${file}: ${m[0]}`))).toEqual([])
})

test('SDK styles size text with the UI type tiers',()=>{
 expect(declarations('font-size').filter(value=>!/: var\(--ui-font-size(?:-sm|-lg|-title)?\b/.test(value))).toEqual([])
})

test('every UI token an SDK style reads without a fallback exists in the UI theme',()=>{
 const read=styles.flatMap(({file,css})=>[...css.matchAll(/var\((--ui-[\w-]+)\s*\)/g)].map(m=>({file,token:m[1]})))
 expect(read.filter(({token})=>!defined.has(token)).map(({file,token})=>`${file}: ${token}`)).toEqual([])
})

// The background panel's button row sits between paragraphs and job cards that
// carry no margin of their own, so the row keeps a UI space above and below it;
// inside a card the card's own padding and gap space it.
test('the background button row keeps a UI space from the text and cards around it',()=>{
 const css=styles.find(({file})=>file==='background/BackgroundPanel.vue')!.css
 expect(css.match(/(?:^|\})\s*\.integration-actions\s*\{([^}]*)\}/)?.[1]).toMatch(/margin-block\s*:\s*var\(--ui-space-\d/)
 expect(css.match(/\.bg-card-body\s*>\s*\.integration-actions\s*\{([^}]*)\}/)?.[1]).toMatch(/margin-block\s*:\s*0/)
})
