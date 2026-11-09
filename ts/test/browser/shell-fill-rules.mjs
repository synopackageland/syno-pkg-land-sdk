// Real-browser check for AppShell's fill-page child rules. jsdom cannot match
// `:nth-child(… of S)`, so this compares, in Chromium, the shipped rules with
// the earlier selectors they replace (a later-sibling rule and a
// `.app-page-content:has(...)` rule that restyled the whole page): for each
// layout, every element under the page content must get the same display,
// flex and min-height. It also checks a few values directly.
// playwright-core is not a dependency: npm i --no-save playwright-core@1.58.2 first.
import {readFileSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import {compileStyle,parse} from '@vue/compiler-sfc'

const {chromium}=await import('playwright-core').catch(()=>{console.error('shell fill rules: run npm i --no-save playwright-core@1.58.2 first');process.exit(2)})
const id='data-v-fill'
const FILL='[data-ui-section-tabs-fill], [data-ui-settings-page]'
const current=readFileSync(fileURLToPath(new URL('../../src/shell/AppShell.vue',import.meta.url)),'utf8')
// The rules as they were, applied to the same source.
const replacements=[
 [`.app-page-fill .app-page-content > :deep(:not(:empty, [hidden]):where(:nth-child(n + 2 of :not(:empty, [hidden])))) { flex: none; }`,
  `.app-page-fill .app-page-content > :deep(:not(:empty, [hidden]) ~ *) { flex: none; }`],
 [`.app-page-fill .app-page-content > :deep(:not(${FILL}):not(:has(${FILL})):is(:has(~ :is(${FILL}), ~ * :is(${FILL})), :is(${FILL}, :has(${FILL})) ~ *)) { flex: none; }`,
  `.app-page-fill .app-page-content:has(${FILL}) > :deep(:not(${FILL}):not(:has(${FILL}))) { flex: none; }`],
]
let previous=current
for(const [now,before] of replacements){
 if(!previous.includes(now)){console.error('shell fill rules: AppShell.vue no longer has the rule',now);process.exit(1)}
 previous=previous.replace(now,before)
}
const css=source=>parse(source).descriptor.styles.filter(style=>style.scoped).map(style=>compileStyle({source:style.content,filename:'AppShell.vue',id,scoped:true}).code).join('\n')

const S='<div data-ui-settings-page><p>Settings</p></div>'
const T='<div class="ui-section-tabs-fill" data-ui-section-tabs-fill><nav>Tabs</nav><div class="ui-tab-panel"><p>Tab</p></div></div>'
const layouts={
 'plain page': '<p>Intro</p><div>Body</div><div></div><p hidden>Off</p><div>More</div>',
 'empty and hidden nodes first': '<div></div><p hidden>Off</p><div>Body</div><div>More</div>',
 'fill first': `${S}<p>After</p><div></div>`,
 'fill in the middle': `<p>Note</p>${T}<div>After</div>`,
 'fill last': `<p>Note</p><div>Callout</div>${S}`,
 'fill nested in App wrappers': `<p>Note</p><div class="app"><div>Error</div><div data-page="settings"><h2>Heading</h2>${T}</div></div><div>Toasts</div>`,
 'hidden fill and a shown one': `<p>Note</p><div hidden>${S}</div><div>${T}</div><div></div>`,
 'two fills': `<p>Note</p>${S}<div>Between</div><div>${T}</div><p>End</p>`,
 'only a fill': S,
 'fill then empty and hidden nodes': `${T}<div></div><p hidden>Off</p>`,
}

const browser=await chromium.launch(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{})
const failures=[]
const fail=msg=>{failures.push(msg);console.error('FAIL',msg)}
try{
 const page=await browser.newPage()
 const read=async(style,body)=>{
  await page.setContent(`<style>${style}</style><div class="app-page app-page-fill" ${id}><div class="app-page-content" ${id}>${body}</div></div>`)
  return page.evaluate(()=>[...document.querySelectorAll('.app-page-content *')].map((e,i)=>{
   const s=getComputedStyle(e)
   return {at:`${i}:${e.tagName.toLowerCase()}${e.className?'.'+e.className:''}`,display:s.display,flex:`${s.flexGrow} ${s.flexShrink} ${s.flexBasis}`,minHeight:s.minHeight}
  }))
 }
 const now=css(current),before=css(previous)
 if(now===before)fail('the earlier rules were not substituted')
 for(const [name,body] of Object.entries(layouts)){
  const a=await read(now,body),b=await read(before,body)
  if(JSON.stringify(a)!==JSON.stringify(b))fail(`${name}: computed styles differ\n now:    ${JSON.stringify(a)}\n before: ${JSON.stringify(b)}`)
 }
 // Direct values: the first child that draws takes the height; with a fill
 // component, only the child that is or holds it does.
 const grow=async(body,selector)=>{await read(now,body);return page.evaluate(s=>getComputedStyle(document.querySelector(`.app-page-content > ${s}`)).flexGrow,selector)}
 const expect=async(name,body,selector,value)=>{const got=await grow(body,selector);if(got!==value)fail(`${name}: ${selector} flex-grow ${got}, want ${value}`)}
 await expect('plain page','<div></div><p class="a">A</p><p class="b">B</p>','.a','1')
 await expect('plain page','<div></div><p class="a">A</p><p class="b">B</p>','.b','0')
 await expect('fill nested',`<p class="a">A</p><div class="w"><div>${S}</div></div><p class="b">B</p>`,'.a','0')
 await expect('fill nested',`<p class="a">A</p><div class="w"><div>${S}</div></div><p class="b">B</p>`,'.w','1')
 await expect('fill nested',`<p class="a">A</p><div class="w"><div>${S}</div></div><p class="b">B</p>`,'.b','0')
}finally{await browser.close()}
if(failures.length){console.error(`shell fill rules: ${failures.length} failure(s)`);process.exit(1)}
console.log(`shell fill rules: ok (${Object.keys(layouts).length} layouts)`)
