// @vitest-environment jsdom
// A fill page's content gives its height to the page, not to whatever node
// the slot happens to render first. jsdom has no layout and does not compute
// the flex shorthand: read the last matching declaration of AppShell's own
// scoped style (its later fill rules are at least as specific).
import {readFileSync} from 'node:fs'
import {compileStyle,parse} from '@vue/compiler-sfc'
import {afterEach,expect,test} from 'vitest'

const id='data-v-fill'
const source=readFileSync('src/shell/AppShell.vue','utf8')
const css=parse(source).descriptor.styles.filter(style=>style.scoped).map(style=>compileStyle({source:style.content,filename:'AppShell.vue',id,scoped:true}).code).join('\n')

function content(children:string){
 const sheet=document.createElement('style');sheet.textContent=css;document.head.append(sheet)
 const page=document.createElement('div');page.className='app-page app-page-fill';page.setAttribute(id,'')
 page.innerHTML=`<div class="app-page-content" ${id}>${children}</div>`
 document.body.append(page)
 return (selector:string)=>{
  const element=page.querySelector(selector)!
  let flex=''
  for(const rule of Array.from(sheet.sheet!.cssRules) as CSSStyleRule[]){
   const value=/(?:^|[{;]\s*)flex:\s*([^;}]+)/.exec(rule.cssText)?.[1]?.trim()
   if(value&&element.matches(rule.selectorText)) flex=value
  }
  return flex
 }
}
afterEach(()=>{document.head.innerHTML='';document.body.innerHTML=''})

test('the content is a flex column, not a one-row grid',()=>{
 content('<main class="page">Page</main>')
 const style=getComputedStyle(document.querySelector('.app-page-content')!)
 expect(style.display).toBe('flex')
 expect(style.flexDirection).toBe('column')
})

test.each([
 ['an empty node before the page','<span class="extra"></span><main class="page">Page</main>'],
 ['a hidden node before the page','<span class="extra" hidden>x</span><main class="page">Page</main>'],
 ['a node after the page','<main class="page">Page</main><div class="extra">Toast host</div>'],
 ['an empty node after the page','<main class="page">Page</main><div class="extra"></div>'],
])('with %s, the page takes the height and the other node keeps its size',(_,children)=>{
 const flex=content(children)
 expect(flex('.page')).toBe('1 1 0%')
 expect(flex('.extra')).toBe('0 0 auto')
})
