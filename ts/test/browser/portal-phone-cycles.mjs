// Real-browser check that DSM's portal header on a phone survives repeated
// hide and show (portal-phone.html, motion on): five touch drags down until the
// header hides and back up until it shows, then flings both ways. The header
// fades in on the way back; the account it shows must not move into the App's
// bar meanwhile (a bar that then gives way to the header, which brings the bar
// back: a loop that never yields the main thread, SDK 0.53.4). Checks every
// frame keeps coming (no gap over 200ms, page.evaluate answers), that on every
// frame the App's content starts at the row's bottom (a flick moves both at
// the UI's faster speed), and the end
// states: the header shown at the top with the App's content under it, or the
// App's own bar in its place.
// Tab pages without a phone bar (Home, Meetings) and pages with one (a tab page
// with header actions, a sub-page) are covered.
// --browser webkit runs WebKit, which has no touch input here: scrollBy steps
// stand in for drags and large quick steps for flings.
// playwright-core is not a dependency: npm i --no-save playwright-core@1.58.2 first.
import assert from 'node:assert/strict'
import {fileURLToPath} from 'node:url'
import {join} from 'node:path'
import {createServer} from 'vite'
const playwright=await import('playwright-core').catch(()=>{console.error('portal cycles: run pnpm add --save-dev playwright-core first');process.exit(2)})
const here=fileURLToPath(new URL('.',import.meta.url))
const engine=process.argv.includes('--browser')?process.argv[process.argv.indexOf('--browser')+1]:'chromium'
const server=await createServer({configFile:join(here,'vite.config.ts')})
await server.listen()
const browser=await playwright[engine].launch({headless:true,...(engine==='chromium'&&process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{})})
const wait=ms=>new Promise(resolve=>setTimeout(resolve,ms))
// Resolves to 'timeout' when the page stops answering.
const within=(promise,ms)=>Promise.race([promise,wait(ms).then(()=>'timeout')])
// Every animation frame's time since the last one; the longest gap is the
// longest the main thread was held.
// Each frame also records where DSM's row and the App's content are drawn
// while neither the App's bar is shown: the content follows the row's bottom.
const frames=()=>{
 window.__gaps=[];window.__apart=[];let last=performance.now()
 const tl=document.querySelector('.sds-standalone-main-window > .x-window-tl'),main=document.querySelector('[data-ui-layout-main] > main')
 const tick=now=>{
  window.__gaps.push(now-last);last=now
  if(!document.querySelector('[data-ui-mobile-navigation]:not([hidden])')){
   const bottom=Math.max(tl.getBoundingClientRect().bottom,0),content=main.getBoundingClientRect().top
   if(Math.abs(content-bottom)>2)window.__apart.push([Math.round(bottom),Math.round(content)])
  }
  window.__tick=requestAnimationFrame(tick)
 }
 window.__tick=requestAnimationFrame(tick)
}
const state=page=>within(page.evaluate(()=>{
 const main=document.querySelector('.sds-standalone-main-window'),tl=main.querySelector(':scope > .x-window-tl')
 const row=tl.getBoundingClientRect(),content=document.querySelector('[data-ui-layout-main] > main').getBoundingClientRect()
 const bar=document.querySelector('[data-ui-mobile-navigation]:not([hidden])')
 const hit=document.elementFromPoint(innerWidth/2,Math.max(row.top,0)+row.height/2)
 const scroller=[...document.querySelectorAll('.app-page')].find(element=>element.offsetParent)
 return {collapsed:main.getAttribute('data-synopkgland-portal-collapsed'),top:Math.round(row.top),bottom:Math.round(row.bottom),opacity:Number(getComputedStyle(tl).opacity),
  onTop:!!hit&&tl.contains(hit),content:Math.round(content.top),bar:bar?Math.round(bar.getBoundingClientRect().top):null,scrollTop:Math.round(scroller.scrollTop),
  maxGap:Math.round(Math.max(0,...(window.__gaps??[]))),frames:window.__gaps?.length??0,apart:window.__apart??[]}
}),5000)
async function settle(page){await wait(700);for(let i=0;i<3;i++){const s=await state(page);if(s==='timeout')return s;const again=await state(page);if(again==='timeout'||(again.collapsed===s.collapsed&&again.top===s.top&&again.content===s.content))return again}return state(page)}
try{
 for(const [query,label,opener] of [['portal=1','home'],['portal=1','meetings','meetings'],['portal=1&actions=1','home-actions'],['portal=1','detail','detail']]){
  const context=await browser.newContext({viewport:{width:390,height:844},isMobile:engine!=='firefox',hasTouch:true,deviceScaleFactor:2})
  const page=await context.newPage(),errors=[]
  page.on('pageerror',error=>errors.push(error.message))
  await page.goto(`${server.resolvedUrls.local[0]}portal-phone.html?${query}`)
  await page.waitForSelector('[data-ready]');await wait(400)
  if(opener){await page.locator('[data-ui-tab-bar-item="meetings"]').tap();await wait(300)}
  if(opener==='detail'){await page.locator('[data-row="0"]').click();await wait(400)}
  const cdp=engine==='chromium'?await context.newCDPSession(page):null
  // A drag: one finger moved steps times by delta px, 16ms apart (delta < 0 scrolls down).
  const drag=async(y,delta,steps,gap=16)=>{
   if(!cdp){for(let i=0;i<steps;i++){if(await within(page.evaluate(d=>[...document.querySelectorAll('.app-page')].find(e=>e.offsetParent).scrollBy(0,d),-delta),3000)==='timeout')return 'timeout';await wait(gap)}return}
   const send=(type,points)=>within(cdp.send('Input.dispatchTouchEvent',{type,touchPoints:points}),3000)
   if(await send('touchStart',[{x:195,y}])==='timeout')return 'timeout'
   for(let i=1;i<=steps;i++){if(await send('touchMove',[{x:195,y:y+delta*i}])==='timeout')return 'timeout';await wait(gap)}
   return send('touchEnd',[])
  }
  const bar=opener==='detail'||query.includes('actions')
  const rest=await settle(page)
  if(bar){assert.equal(rest.collapsed,'bar',`${label}: the App's bar in the header's place`);assert.equal(rest.bar,0)}
  else{assert.equal(rest.collapsed,null,`${label}: DSM's header shown`);assert.equal(rest.content,rest.bottom);assert.equal(rest.onTop,true)}
  await page.evaluate(frames)
  const cycles=[...Array.from({length:5},()=>[[600,-30,14],[300,40,14]]).flat(),[650,-90,5,8],[200,120,5,8],[650,-90,5,8],[200,120,5,8]]
  for(const [i,[y,delta,steps,gap]] of cycles.entries()){
   assert.notEqual(await drag(y,delta,steps,gap),'timeout',`${label} drag ${i}: the page takes touch input`)
   const s=await settle(page)
   assert.notEqual(s,'timeout',`${label} drag ${i}: the page answers`)
   assert.ok(s.maxGap<200,`${label} drag ${i}: no frame held over 200ms (${s.maxGap}ms)`)
   assert.deepEqual(s.apart,[],`${label} drag ${i}: every frame draws the App's content at DSM's row bottom ([row, content])`)
   if(delta>0&&s.scrollTop===0){
    // Back at the top: the header (or the App's bar) shown, the content under it.
    if(bar){assert.equal(s.collapsed,'bar',`${label} drag ${i}: the App's bar stays`);assert.equal(s.bar,0,`${label} drag ${i}: the App's bar at the top`)}
    else{assert.equal(s.collapsed,null,`${label} drag ${i}: DSM's header back`);assert.equal(s.top,0);assert.equal(s.opacity,1);assert.equal(s.onTop,true);assert.equal(s.content,s.bottom,`${label} drag ${i}: content under the header`)}
   }else if(delta<0&&!bar){
    assert.equal(s.collapsed,'scroll',`${label} drag ${i}: DSM's header hidden on scroll`);assert.ok(s.bottom<=0);assert.equal(s.content,0,`${label} drag ${i}: content at the top`)
   }
  }
  // Finally scroll to the top: the end state at rest.
  for(let i=0;i<3;i++)await drag(250,60,14)
  const end=await settle(page)
  assert.notEqual(end,'timeout');assert.equal(end.scrollTop,0)
  if(bar)assert.equal(end.collapsed,'bar');else{assert.equal(end.collapsed,null);assert.equal(end.content,end.bottom);assert.equal(end.onTop,true)}
  assert.deepEqual(errors,[])
  console.log(`portal cycles: ${label} ok (${end.frames} frames, longest ${end.maxGap}ms)`)
  await context.close()
 }
}finally{await browser.close();await server.close()}
