// Real-browser check of the phone's one top bar (portal-phone.html): in DSM's
// Application Portal a tab page shows only DSM's compact header row, which
// hides with the App's bars on scroll; a drill-down page's bar (Back, title,
// account) and an App's header actions take that row's place; outside the
// portal a tab page draws a bar only for the account or actions. Desktop
// widths keep DSM's header. --shots <dir> saves screenshots.
// playwright-core is not a dependency: npm i --no-save playwright-core@1.58.2 first.
import assert from 'node:assert/strict'
import {mkdirSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import {join} from 'node:path'
import {createServer} from 'vite'
const {chromium}=await import('playwright-core').catch(()=>{console.error('portal phone: run npm i --no-save playwright-core@1.58.2 first');process.exit(2)})
const here=fileURLToPath(new URL('.',import.meta.url))
const at=process.argv.indexOf('--shots'),shots=at<0?undefined:process.argv[at+1]
if(shots)mkdirSync(shots,{recursive:true})
const server=await createServer({configFile:join(here,'vite.config.ts')})
await server.listen()
const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{})})
const state=page=>page.evaluate(()=>{
 const main=document.querySelector('.sds-standalone-main-window'),tl=main?.querySelector('.x-window-tl')
 const app=document.querySelector('[data-ui-app]').getBoundingClientRect(),bar=document.querySelector('[data-ui-mobile-navigation]')
 const tab=document.querySelector('[data-ui-tab-bar]')?.getBoundingClientRect()
 return {collapsed:main?.getAttribute('data-synopkgland-portal-collapsed')??null,dsmBottom:tl?Math.round(tl.getBoundingClientRect().bottom):null,
  appTop:Math.round(app.top),appBottom:Math.round(app.bottom),bar:!!bar,barBottom:bar?Math.round(bar.getBoundingClientRect().bottom):null,
  title:bar?.querySelector('[data-ui-mobile-title]')?.textContent??null,back:!!bar?.querySelector('[data-ui-mobile-back]'),appIcon:!!bar?.querySelector('[data-ui-mobile-app-icon]'),
  account:bar?.querySelector('[data-ui-mobile-action][aria-label="administrator"]')?'icon':bar?.querySelector('[data-ui-mobile-more]')?'more':null,
  tabBottom:tab?Math.round(tab.bottom):null,overflow:document.scrollingElement.scrollWidth>innerWidth}
})
async function scrollDown(page){
 await page.mouse.move(200,450)
 for(let i=0;i<5;i++){await page.mouse.wheel(0,120);await page.waitForTimeout(60)}
 await page.waitForTimeout(400)
}
try{
 for(const [width,height] of [[390,844],[402,874]]){
  for(const query of ['portal=1','portal=1&actions=1','portal=0','portal=0&user=']){
   const context=await browser.newContext({viewport:{width,height},isMobile:true,hasTouch:true,deviceScaleFactor:2,reducedMotion:'reduce'})
   const page=await context.newPage(),errors=[]
   page.on('pageerror',error=>errors.push(error.message))
   await page.goto(`${server.resolvedUrls.local[0]}portal-phone.html?${query}`)
   await page.waitForSelector('[data-ready]');await page.waitForTimeout(400)
   const portal=query.startsWith('portal=1'),actions=query.includes('actions'),user=!query.includes('user=')
   const label=`${width}-${portal?'portal':'standalone'}${actions?'-actions':''}${user?'':'-no-account'}`
   const shot=async name=>{if(shots)await page.screenshot({path:join(shots,`${label}-${name}.png`)})}
   // A tab page: no title row, no duplicated App icon, one bar at most.
   let s=await state(page)
   assert.equal(s.overflow,false,`${label}: no horizontal overflow`)
   assert.equal(s.title||null,null,`${label}: a tab page draws no title`)
   if(portal&&!actions){
    assert.equal(s.bar,false,`${label}: DSM's row is the one bar`)
    assert.equal(s.collapsed,null);assert.equal(s.appTop,s.dsmBottom,`${label}: the App starts under DSM's row`)
   }else if(actions||user){
    assert.equal(s.bar,true,`${label}: one bar for the account or actions`)
    assert.equal(s.appTop,0,`${label}: the App's bar is at the top`)
    if(portal)assert.equal(s.collapsed,'bar',`${label}: DSM's row gives way`)
    assert.equal(s.account,actions?'more':'icon',`${label}: one account control`)
   }else assert.equal(s.bar,false,`${label}: nothing for a bar, no bar`)
   assert.equal(s.appBottom,height,`${label}: the App reaches the bottom`)
   assert.equal(s.tabBottom,height,`${label}: tab bar at the bottom`)
   await shot('1-tab')
   // Scrolling hides the one bar, DSM's row too, and the App takes the room.
   await scrollDown(page)
   s=await state(page)
   assert.ok(s.tabBottom>height,`${label}: tab bar hidden on scroll`)
   if(portal){assert.ok(s.collapsed,`${label}: DSM's row hidden on scroll`);assert.equal(s.appTop,0)}
   if(s.bar)assert.ok(s.barBottom<=0,`${label}: the App's bar hidden on scroll (${s.barBottom})`)
   await shot('2-tab-scrolled')
   // Back up: the row returns.
   await page.mouse.wheel(0,-400);await page.waitForTimeout(500)
   s=await state(page)
   if(portal&&!actions){assert.equal(s.collapsed,null,`${label}: DSM's row back`);assert.equal(s.appTop,s.dsmBottom)}
   // Drill down: one row with Back and the title, in place of DSM's.
   await page.locator('[data-ui-tab-bar-item="meetings"]').tap();await page.waitForTimeout(300)
   await page.locator('[data-row="0"]').click();await page.waitForTimeout(400)
   s=await state(page)
   assert.equal(s.bar,true);assert.equal(s.back,true);assert.equal(s.title,'Weekly sync',`${label}: sub-page title`)
   assert.equal(s.appTop,0,`${label}: one row at the top`)
   if(portal)assert.equal(s.collapsed,'bar')
   if(user)assert.ok(s.account,`${label}: the account in the sub-page's bar`)
   await shot('3-detail')
   if(user){
    if(s.account==='icon')await page.locator('[data-ui-mobile-action][aria-label="administrator"]').tap()
    else{await page.locator('[data-ui-mobile-more]').tap();await page.getByRole('menuitem',{name:'administrator'}).tap()}
    await page.getByRole('dialog').waitFor();await page.waitForTimeout(500)
    await shot('4-account')
    await page.getByRole('dialog').getByRole('button',{name:'Sign out'}).tap()
    assert.equal(await page.evaluate(()=>document.documentElement.dataset.signedOut),'1',`${label}: Sign out reaches the host`)
   }
   await page.locator('[data-ui-mobile-back]').tap();await page.waitForTimeout(400)
   s=await state(page)
   if(portal&&!actions)assert.equal(s.collapsed,null,`${label}: DSM's row back after Back`)
   assert.deepEqual(errors,[])
   console.log(`portal phone: ${label} ok`)
   await context.close()
  }
 }
 // Desktop: DSM's header and the App's own layout as before.
 const context=await browser.newContext({viewport:{width:1280,height:800},deviceScaleFactor:1})
 const page=await context.newPage()
 await page.goto(`${server.resolvedUrls.local[0]}portal-phone.html?portal=1`)
 await page.waitForSelector('[data-ready]');await page.waitForTimeout(300)
 const s=await state(page)
 assert.equal(s.collapsed,null);assert.equal(s.bar,false);assert.equal(s.appTop,s.dsmBottom)
 assert.equal(await page.evaluate(()=>!!document.querySelector('[data-synopkgland-portal-mobile]')),false)
 if(shots)await page.screenshot({path:join(shots,'1280-portal.png')})
 console.log('portal phone: 1280 ok')
 await context.close()
}finally{await browser.close();await server.close()}
