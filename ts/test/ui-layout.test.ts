import {readdirSync,readFileSync} from 'node:fs'
import {join,relative} from 'node:path'
import {expect,test} from 'vitest'
// UI 0.10 lays out a page's direct children with --ui-section-gap and sizes a
// Dialog without `width` to its content (420–760px, like a DSM message box).
const src=join(__dirname,'..','src')
const vue=(dir:string):string[]=>readdirSync(dir,{withFileTypes:true}).flatMap(e=>e.isDirectory()?vue(join(dir,e.name)):e.name.endsWith('.vue')?[join(dir,e.name)]:[])
const read=(file:string)=>readFileSync(join(src,file),'utf8')
const rule=(source:string,selector:string)=>source.match(new RegExp(`(?:^|\\n)\\s*\\${selector}\\s*\\{([^}]*)\\}`))?.[1]??''

test('blocks AppShell places in PageContent carry no margin of their own',()=>{
 expect(rule(read('shell/AppShell.vue'),'.app-notices')).not.toMatch(/margin/)
 expect(rule(read('delegate/ExecutorNotice.vue'),'.executor-notice')).not.toMatch(/margin/)
})

test('SDK dialogs size to their content; only the NAS picker, wider than the cap, sets a width',()=>{
 const fixed=vue(src).filter(file=>/<Dialog\b[^>]*\swidth=/.test(readFileSync(file,'utf8'))).map(file=>relative(src,file))
 expect(fixed).toEqual(['nas/NasPicker.vue'])
})
