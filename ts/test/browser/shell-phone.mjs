// Focused real-browser contract check; --shots <dir> saves review evidence.
import assert from 'node:assert/strict'
import {mkdirSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import {join} from 'node:path'
import {createServer} from 'vite'
const {chromium}=await import('playwright-core')
const here=fileURLToPath(new URL('.',import.meta.url))
const at=process.argv.indexOf('--shots'),shots=at<0?undefined:process.argv[at+1]
const baseline=process.argv.includes('--baseline')
if(shots)mkdirSync(shots,{recursive:true})
const server=await createServer({configFile:join(here,'vite.config.ts')})
await server.listen()
const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{})})
try{
 for(const [width,fullPage] of [[390,true],[1280,true],[1280,false]]){
  if(baseline&&width===390)continue
  const context=await browser.newContext({viewport:{width,height:width===390?844:800},isMobile:width===390,hasTouch:width===390,deviceScaleFactor:1,reducedMotion:'reduce'})
  const page=await context.newPage(),errors=[]
  page.on('pageerror',error=>errors.push(error.message))
  await page.goto(`${server.resolvedUrls.local[0]}phone.html?window=${fullPage?'0':'1'}`)
  await page.waitForSelector('[data-phone]')
  await page.waitForTimeout(600)
  assert.deepEqual(errors,[])
  assert.equal(await page.evaluate(()=>document.scrollingElement.scrollWidth>innerWidth),false,'no horizontal document overflow')
  if(!baseline){
   assert.equal(await page.locator('[data-phone]').getAttribute('data-phone'),String(width===390))
   assert.equal(await page.locator('[data-ui-mobile-menu]').count(),0,'no duplicate menu button')
   assert.equal(await page.locator('[data-ui-tab-bar]').count(),width===390?1:0)
   assert.equal(await page.locator('[data-ui-layout-mode]').getAttribute('data-ui-layout-mode'),width===390?'mobile':fullPage?'web':'window')
  }
  const label=`${width}-${fullPage?'web':'window'}`
  if(shots)await page.screenshot({path:join(shots,`${baseline?'baseline-':''}${label}.png`)})
  if(width===390){
   assert.equal(await page.locator('[data-ui-mobile-title]').innerText(),'My library')
   await page.locator('[data-ui-mobile-action]').tap()
   assert.equal(await page.evaluate(()=>document.documentElement.dataset.headerAction),'refresh')
   await page.locator('[data-ui-mobile-more]').tap()
   await page.getByRole('menuitem',{name:'Settings',exact:true}).tap()
   assert.equal(await page.evaluate(()=>document.documentElement.dataset.headerAction),'settings')
   await page.locator('[data-ui-tab-bar-item="books"]').tap()
   assert.equal(await page.locator('[data-ui-tab-bar-item="books"]').getAttribute('aria-current'),'page')
   await page.locator('[data-ui-tab-bar-more]').tap()
   await page.locator('[data-ui-nav-drawer][data-state="open"]').waitFor()
   await page.locator('[data-ui-nav-drawer]').getByRole('button',{name:'Settings',exact:true}).tap()
   assert.equal(await page.locator('h1.app-page-title').innerText(),'Settings')
   await page.locator('[data-ui-nav-drawer][data-state="open"]').waitFor({state:'hidden'})
   // The same signal follows the container after a phone is widened.
   await page.setViewportSize({width:1280,height:800})
   await page.waitForFunction(()=>document.querySelector('[data-phone]')?.getAttribute('data-phone')==='false')
   assert.equal(await page.locator('[data-ui-tab-bar]').count(),0)
  }
  if(!baseline){
   await page.setViewportSize({width,height:width===390?844:800})
   await page.goto(`${server.resolvedUrls.local[0]}phone.html?window=${fullPage?'0':'1'}&accessory=1`)
   await page.locator('[data-ui-bottom-accessory]').waitFor()
   await page.waitForTimeout(400)
   assert.equal(await page.locator('[data-running-task]').innerText(),`Running task: home (${width===390?'phone':'desktop'})`)
   const bounds=await page.evaluate(()=>{
    const accessory=document.querySelector('[data-ui-bottom-accessory]').getBoundingClientRect()
    const content=document.querySelector('.app-page').getBoundingClientRect()
    const tabs=document.querySelector('[data-ui-tab-bar]')?.getBoundingClientRect()
    return {top:accessory.top,bottom:accessory.bottom,height:accessory.height,pageBottom:content.bottom,tabTop:tabs?.top,viewport:innerHeight}
   })
   assert.ok(bounds.height>=48&&bounds.height<=49,'accessory wraps the content and its border')
   assert.ok(bounds.bottom<=bounds.viewport+1,'accessory remains inside the viewport')
   assert.ok(Math.abs(bounds.bottom-(bounds.tabTop??bounds.viewport))<=1,'accessory docks above phone tabs or at the desktop bottom')
   assert.ok(bounds.pageBottom<=bounds.top+1,'page ends above the accessory')
   assert.equal(await page.locator('.app-page [data-running-task]').count(),0,'accessory stays outside the page scroller')
   if(shots)await page.screenshot({path:join(shots,`${label}-accessory.png`)})
   await page.evaluate(()=>{window.shellAccessory.value=false})
   await page.locator('[data-ui-bottom-accessory]').waitFor({state:'hidden'})
   console.log(`shell accessory: ${label} ok`)
  }
  console.log(`shell phone: ${label} ok${baseline?' (baseline)':''}`)
  await context.close()
 }
}finally{await browser.close();await server.close()}
