// @vitest-environment jsdom
// AppShell clips AppLayout's own <main>, where the page scrolls; a <main> an App
// renders inside its page is the App's and keeps the overflow it sets. Reads
// AppShell's own scoped style, as app-page-fill.test.ts does.
import {readFileSync} from 'node:fs'
import {compileStyle,parse} from '@vue/compiler-sfc'
import {afterEach,expect,test} from 'vitest'

const id='data-v-main'
const source=readFileSync('src/shell/AppShell.vue','utf8')
const css=parse(source).descriptor.styles.filter(style=>style.scoped).map(style=>compileStyle({source:style.content,filename:'AppShell.vue',id,scoped:true}).code).join('\n')
afterEach(()=>{document.head.innerHTML='';document.body.innerHTML=''})

test('only the layout main is clipped, not a main in the App page',()=>{
 const sheet=document.createElement('style');sheet.textContent=css;document.head.append(sheet)
 const shell=document.createElement('div');shell.className='app-shell';shell.setAttribute(id,'')
 // AppLayout's DOM: a sidebar and the main column, whose <main> holds the page.
 shell.innerHTML=`<aside></aside><div data-ui-layout-main><main class="layout"><div class="app-page" ${id}><div class="app-page-content" ${id}><div class="editor"><main class="app">Canvas</main></div></div></div></main></div>`
 document.body.append(shell)
 const hidden=(selector:string)=>{
  const element=shell.querySelector(selector)!
  return (Array.from(sheet.sheet!.cssRules) as CSSStyleRule[]).some(rule=>/overflow:\s*hidden/.test(rule.cssText)&&element.matches(rule.selectorText))
 }
 expect(hidden('main.layout')).toBe(true)
 expect(hidden('main.app')).toBe(false)
})
