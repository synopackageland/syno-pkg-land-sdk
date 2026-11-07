// Real-browser check of the phone's one top bar (portal-phone.html): in DSM's
// Application Portal a tab page shows only DSM's compact header row, which
// hides with the App's bars on scroll by transform only: the window body is
// sized once, the App's page scroller keeps its size, nothing writes
// scrollTop, and the App's content moves up into the header's room. A
// drill-down page's bar (Back, title, account) and an App's header actions
// take that row's place; outside the portal a tab page draws a bar only for
// the account or actions. Desktop and landscape widths keep DSM's header.
// --shots <dir> saves screenshots; --browser webkit runs WebKit.
// playwright-core is not a dependency: npm i --no-save playwright-core@1.58.2 first.
import assert from 'node:assert/strict'
import {mkdirSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import {join} from 'node:path'
import {createServer} from 'vite'
const playwright=await import('playwright-core').catch(()=>{console.error('portal phone: run npm i --no-save playwright-core@1.58.2 first');process.exit(2)})
const here=fileURLToPath(new URL('.',import.meta.url))
const at=process.argv.indexOf('--shots'),shots=at<0?undefined:process.argv[at+1]
const engine=process.argv.includes('--browser')?process.argv[process.argv.indexOf('--browser')+1]:'chromium'
if(shots)mkdirSync(shots,{recursive:true})
const server=await createServer({configFile:join(here,'vite.config.ts')})
await server.listen()
const browser=await playwright[engine].launch({headless:true,...(engine==='chromium'&&process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{})})
// Counts scrollTop writes and page scroller resizes from the moment watch() is called.
const instrument=()=>{
 const own=Object.getOwnPropertyDescriptor(Element.prototype,'scrollTop')
 Object.defineProperty(Element.prototype,'scrollTop',{configurable:true,get(){return own.get.call(this)},set(value){window.__scrollWrites=(window.__scrollWrites??0)+1;own.set.call(this,value)}})
 window.__watch=()=>{
  window.__scrollWrites=0;window.__resizes=0
  let first=true
  window.__observer?.disconnect()
  window.__observer=new ResizeObserver(()=>{if(first)first=false;else window.__resizes++})
  window.__observer.observe(document.querySelector('.app-page'))
 }
}
const state=page=>page.evaluate(()=>{
 const main=document.querySelector('.sds-standalone-main-window'),tl=main?.querySelector('.x-window-tl'),body=main?.querySelector('.x-window-body')
 const app=document.querySelector('[data-ui-app]').getBoundingClientRect(),bar=document.querySelector('[data-ui-mobile-navigation]:not([hidden])')
 const content=document.querySelector('[data-ui-layout-main] > main').getBoundingClientRect()
 const tab=document.querySelector('[data-ui-tab-bar]')?.getBoundingClientRect(),scroller=document.querySelector('.app-page')
 const overflow=[document.scrollingElement,...document.querySelectorAll('[data-ui-layout-main] > main, .app-page')].some(element=>element.scrollWidth>element.clientWidth)
 return {collapsed:main?.getAttribute('data-synopkgland-portal-collapsed')??null,dsmBottom:tl?Math.round(tl.getBoundingClientRect().bottom):null,
  dsmOpacity:tl?Number(getComputedStyle(tl).opacity):null,
  // What layout gives: the header's place, the body's size and the scroller's size.
  layout:{dsm:tl?.offsetTop??null,body:body?[body.offsetTop,body.offsetHeight]:null,scroller:[scroller.offsetHeight,scroller.clientHeight,scroller.clientWidth]},
  appTop:Math.round(app.top),appBottom:Math.round(app.bottom),contentTop:Math.round(content.top),bar:!!bar,barBottom:bar?Math.round(bar.getBoundingClientRect().bottom):null,
  title:bar?.querySelector('[data-ui-mobile-title]')?.textContent??null,back:!!bar?.querySelector('[data-ui-mobile-back]'),appIcon:!!bar?.querySelector('[data-ui-mobile-app-icon]'),
  account:bar?.querySelector('[data-ui-mobile-action][aria-label="administrator"]')?'icon':bar?.querySelector('[data-ui-mobile-more]')?'more':null,
  tabBottom:tab?Math.round(tab.bottom):null,overflow,scrollWrites:window.__scrollWrites,resizes:window.__resizes}
})
// Mobile WebKit has no wheel: scrollBy moves the page scroller as a slow drag would.
async function wheel(page,delta){
 if(engine!=='webkit')return page.mouse.wheel(0,delta)
 await page.evaluate(delta=>document.querySelector('.app-page').scrollBy(0,delta),delta)
}
async function scrollDown(page){
 await page.mouse.move(200,450)
 for(let i=0;i<5;i++){await wheel(page,120);await page.waitForTimeout(60)}
 await page.waitForTimeout(400)
}
try{
 for(const [width,height] of [[390,844],[402,874]]){
  for(const query of ['portal=1','portal=1&actions=1','portal=0','portal=0&user=']){
   const context=await browser.newContext({viewport:{width,height},isMobile:true,hasTouch:true,deviceScaleFactor:2,reducedMotion:'reduce'})
   const page=await context.newPage(),errors=[]
   await page.addInitScript(instrument)
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
    assert.equal(s.collapsed,null);assert.equal(s.contentTop,s.dsmBottom,`${label}: the App's content starts under DSM's row`)
   }else if(actions||user){
    assert.equal(s.bar,true,`${label}: one bar for the account or actions`)
    assert.equal(s.appTop,0,`${label}: the App's bar is at the top`)
    if(portal)assert.equal(s.collapsed,'bar',`${label}: DSM's row gives way`)
    assert.equal(s.account,actions?'more':'icon',`${label}: one account control`)
   }else assert.equal(s.bar,false,`${label}: nothing for a bar, no bar`)
   assert.equal(s.appBottom,height,`${label}: the App reaches the bottom`)
   assert.equal(s.tabBottom,height,`${label}: tab bar at the bottom`)
   await shot('1-tab')
   // Scrolling hides the one bar, DSM's row too, and the App's content takes
   // the room by transform: no size changes, no scrollTop writes.
   const shown=s.layout
   await page.evaluate(()=>window.__watch())
   await scrollDown(page)
   s=await state(page)
   assert.ok(s.tabBottom>height,`${label}: tab bar hidden on scroll`)
   if(portal){
    assert.ok(s.collapsed,`${label}: DSM's row hidden on scroll`)
    assert.ok(s.dsmBottom<=0,`${label}: DSM's row out of the page (${s.dsmBottom})`);assert.equal(s.dsmOpacity,0)
    assert.equal(s.contentTop,0,`${label}: the App's content reaches the top`)
   }
   if(s.bar)assert.ok(s.barBottom<=0,`${label}: the App's bar hidden on scroll (${s.barBottom})`)
   assert.deepEqual(s.layout,shown,`${label}: hiding lays nothing out again`)
   assert.equal(s.resizes,0,`${label}: the page scroller keeps its size`)
   assert.equal(s.scrollWrites,0,`${label}: nothing writes scrollTop`)
   assert.equal(s.overflow,false,`${label}: no horizontal overflow while hidden`)
   await shot('2-tab-scrolled')
   // Back up: the row returns, still without a resize.
   for(let i=0;i<4;i++){await wheel(page,-100);await page.waitForTimeout(60)}
   await page.waitForTimeout(400)
   s=await state(page)
   if(portal&&!actions){assert.equal(s.collapsed,null,`${label}: DSM's row back`);assert.equal(s.contentTop,s.dsmBottom);assert.equal(s.dsmOpacity,1)}
   assert.deepEqual(s.layout,shown,`${label}: showing lays nothing out again`)
   assert.equal(s.resizes,0,`${label}: the page scroller keeps its size on the way back`)
   assert.equal(s.scrollWrites,0)
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
 // Desktop and a phone in landscape (825x360: wider than the phone layout):
 // DSM's header and the App's own layout as before, no sideways pan.
 for(const [width,height,phone] of [[1280,800,false],[825,360,true]]){
  const context=await browser.newContext({viewport:{width,height},deviceScaleFactor:phone?2:1,isMobile:phone&&engine!=='firefox',hasTouch:phone})
  const page=await context.newPage()
  await page.goto(`${server.resolvedUrls.local[0]}portal-phone.html?portal=1`)
  await page.waitForSelector('[data-ready]');await page.waitForTimeout(300)
  const s=await state(page)
  assert.equal(s.collapsed,null);assert.equal(s.bar,false);assert.equal(s.appTop,s.dsmBottom)
  assert.equal(s.overflow,false,`${width}x${height}: no horizontal overflow`)
  assert.equal(await page.evaluate(()=>!!document.querySelector('[data-synopkgland-portal-mobile], [data-synopkgland-portal-overlay]')),false)
  if(shots)await page.screenshot({path:join(shots,`${width}x${height}-portal.png`)})
  console.log(`portal phone: ${width}x${height} ok`)
  await context.close()
 }
}finally{await browser.close();await server.close()}
