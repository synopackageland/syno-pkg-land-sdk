// Real-browser check of DSM's standalone header around AppShell (portal.html):
// in the UI's mobile layout (app root 640px or narrower) the App name gives way
// to its icon and the account button to an account icon whose menu names the
// account; at desktop widths the header is DSM's own. --shots <dir> saves
// screenshots; --baseline only takes them (for the code before this change).
// playwright-core is not a dependency: npm i --no-save playwright-core@1.58.2 first.
import assert from 'node:assert/strict'
import {mkdirSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import {join} from 'node:path'
import {createServer} from 'vite'
const {chromium}=await import('playwright-core').catch(()=>{console.error('portal header: run npm i --no-save playwright-core@1.58.2 first');process.exit(2)})
const here=fileURLToPath(new URL('.',import.meta.url))
const at=process.argv.indexOf('--shots'),shots=at<0?undefined:process.argv[at+1]
const baseline=process.argv.includes('--baseline')
if(shots)mkdirSync(shots,{recursive:true})
const server=await createServer({configFile:join(here,'vite.config.ts')})
await server.listen()
const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{})})
const names={
 en:'Household Photo Library Manager',
 de:'Familienfotobibliotheksverwaltungsanwendung',
 'zh-tw':'家庭相簿與影音資料庫集中管理中心',
 ja:'ファミリーフォトライブラリーマネージャー',
}
const cases=[
 ...[360,390,1280].flatMap(width=>Object.keys(names).map(locale=>({width,locale,user:'administrator'}))),
 {width:360,locale:'en',user:''},
 {width:360,locale:'en',user:'household.media.administrator.account'},
 {width:1280,locale:'en',user:''},
]
const header=page=>page.evaluate(()=>{
 const box=e=>{if(!e)return null;const r=e.getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,width:r.width}}
 const main=document.querySelector('.sds-standalone-main-window')
 const name=document.querySelector('.sds-standalone-main-window-header-text > .x-window-header-text')
 const button=document.querySelector('.sds-standalone-main-window-header__toolbar__btn')
 const text=button?.querySelector('.x-btn-text')
 const range=document.createRange();range.selectNodeContents(name)
 return {mobile:main.hasAttribute('data-synopkgland-portal-mobile'),mode:document.querySelector('[data-ui-layout-mode]')?.getAttribute('data-ui-layout-mode'),
  name:box(name),ink:box(range),nameClip:getComputedStyle(name).overflow,button:box(button),text:box(text),textIndent:text&&getComputedStyle(text).textIndent,
  icon:getComputedStyle(document.querySelector('.x-window-header')).backgroundImage,label:text?.textContent}
})
try{
 for(const {width,locale,user} of cases){
  const mobile=width<=640
  const context=await browser.newContext({viewport:{width,height:mobile?740:800},isMobile:mobile,hasTouch:mobile,deviceScaleFactor:1,reducedMotion:'reduce'})
  const page=await context.newPage(),errors=[]
  page.on('pageerror',error=>errors.push(error.message))
  await page.goto(`${server.resolvedUrls.local[0]}portal.html?${new URLSearchParams({name:names[locale],user,locale:locale==='zh-tw'?'zh-tw':locale})}`)
  await page.waitForSelector('[data-ready]')
  await page.waitForTimeout(300)
  assert.deepEqual(errors,[])
  const label=`${width}-${locale}${user?user==='administrator'?'':'-long-account':'-no-account'}`
  const before=await header(page)
  if(!baseline){
   assert.equal(before.mode,mobile?'mobile':'web',label)
   assert.equal(before.mobile,mobile,`${label}: header marked only in the mobile layout`)
   assert.match(before.icon,/^url\(/,`${label}: the App icon stays`)
   assert.equal(await page.evaluate(()=>document.scrollingElement.scrollWidth>innerWidth),false,`${label}: no horizontal overflow`)
   if(mobile){
    // The name takes no width beyond the icon's padding and draws nothing.
    assert.ok(before.name.width<=34.5,`${label}: name box ${before.name.width}px`)
    assert.equal(before.nameClip,'hidden')
    if(before.button){
     assert.ok(before.name.right<=before.button.left,`${label}: name ends before the account button`)
     assert.ok(before.text.width<=32.5,`${label}: account is an icon (${before.text.width}px)`)
     assert.notEqual(before.textIndent,'0px')
     assert.equal(before.label,user,`${label}: the button keeps the account name for assistive technology`)
    }
   }else{
    // Desktop: DSM's own header, the full name on one line before the account.
    assert.equal(before.nameClip,'visible')
    assert.ok(before.ink.width>100,`${label}: name drawn`)
    if(before.button){
     assert.ok(before.text.width>60,`${label}: account name drawn`)
     assert.equal(before.textIndent,'0px')
    }
   }
  }
  if(shots)await page.screenshot({path:join(shots,`${baseline?'before':'after'}-${label}.png`)})
  if(user){
   await page.locator('.sds-standalone-main-window-header__toolbar__btn .x-btn-text').click()
   await page.waitForTimeout(150)
   const heading=await page.evaluate(()=>{const menu=document.querySelector('.sds-standalone-main-window-header__toolbar__menu');return menu&&getComputedStyle(menu,'::before').content})
   if(!baseline){
    assert.equal(heading,mobile?JSON.stringify(user):'none',`${label}: account menu heading`)
    // The heading sits above Help inside the menu, and the menu stays on screen.
    const menu=await page.evaluate(()=>{const m=document.querySelector('.sds-standalone-main-window-header__toolbar__menu'),r=m.getBoundingClientRect(),help=m.querySelector('.sds-standalone-help').getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,helpTop:help.top,helpBottom:help.bottom}})
    assert.ok(menu.left>=0&&menu.right<=width,`${label}: menu on screen (${menu.left}..${menu.right})`)
    assert.ok(menu.helpBottom<=menu.bottom+0.5,`${label}: Help inside the menu`)
    if(mobile)assert.ok(menu.helpTop-menu.top>=30,`${label}: name above Help`)
   }
   if(shots&&locale==='en')await page.screenshot({path:join(shots,`${baseline?'before':'after'}-${label}-menu.png`)})
   await page.locator('.sds-standalone-main-window-header__toolbar__btn .x-btn-text').click()
  }
  if(!baseline&&mobile&&user){
   // Widening the same page restores DSM's header.
   await page.setViewportSize({width:1280,height:800})
   await page.waitForFunction(()=>!document.querySelector('.sds-standalone-main-window[data-synopkgland-portal-mobile]'))
   assert.equal(await page.evaluate(()=>document.querySelector('[data-synopkgland-portal-account]')),null)
  }
  console.log(`portal header: ${label} ok${baseline?' (baseline)':''}`)
  await context.close()
 }
}finally{await browser.close();await server.close()}
