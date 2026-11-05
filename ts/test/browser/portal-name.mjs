// Real-browser check of the App name in DSM's standalone header (portal.html)
// at desktop and tablet widths: a name that would run under the account button
// is cut with an ellipsis before it, or left out (icon only) when less than an
// icon's width is free; the full name stays the element's text and its
// tooltip. A name that fits is left exactly as DSM draws it. --shots <dir>
// saves screenshots; --baseline only takes them (for the code before this change).
// playwright-core is not a dependency: npm i --no-save playwright-core@1.58.2 first.
import assert from 'node:assert/strict'
import {mkdirSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import {join} from 'node:path'
import {createServer} from 'vite'
const {chromium}=await import('playwright-core').catch(()=>{console.error('portal name: run npm i --no-save playwright-core@1.58.2 first');process.exit(2)})
const here=fileURLToPath(new URL('.',import.meta.url))
const at=process.argv.indexOf('--shots'),shots=at<0?undefined:process.argv[at+1]
const baseline=process.argv.includes('--baseline')
if(shots)mkdirSync(shots,{recursive:true})
const server=await createServer({configFile:join(here,'vite.config.ts')})
await server.listen()
const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{})})
const longName='Household Photo Library, Video Archive and Media Sharing Management Center'
const longUser='household.media.library.administrator.account@example.internal'
const hugeUser='household.media.library.and.video.archive.administrator.account.for.the.whole.family.at.home@example.internal'
// fit: the name is drawn in full; cut: ellipsis before the button; icon: no text.
const cases=[
 ...[800,1024,1280].map(width=>({width,name:longName,user:longUser,expect:width>=1280?'fit':'cut'})),
 {width:1280,name:'Photos',user:'admin',expect:'fit',label:'1280-short'},
 {width:1024,name:longName,user:'',expect:'fit',label:'1024-no-account'},
 {width:720,name:longName,user:hugeUser,expect:'icon',label:'720-huge-account'},
 // The UI's mobile layout keeps its icon-only header.
 {width:600,name:longName,user:longUser,expect:'mobile'},
]
const header=page=>page.evaluate(()=>{
 const main=document.querySelector('.sds-standalone-main-window')
 const name=document.querySelector('.sds-standalone-main-window-header-text > .x-window-header-text')
 const button=document.querySelector('.sds-standalone-main-window-header__toolbar__btn')
 const range=document.createRange();range.selectNodeContents(name)
 const r=name.getBoundingClientRect(),style=getComputedStyle(name),pad=parseFloat(style.paddingLeft)
 return {mobile:main.hasAttribute('data-synopkgland-portal-mobile'),fit:main.getAttribute('data-synopkgland-portal-name'),
  left:r.left,right:r.right,height:r.height,visible:Math.max(0,r.width-pad-parseFloat(style.paddingRight)),ink:range.getBoundingClientRect().width,lines:range.getClientRects().length,
  overflow:style.overflow,ellipsis:style.textOverflow,title:name.getAttribute('title'),text:name.textContent,button:button&&button.getBoundingClientRect().left}
})
try{
 for(const c of cases){
  const {width,name,user,expect}=c,label=c.label??`${width}-long`
  const context=await browser.newContext({viewport:{width,height:800},deviceScaleFactor:1,reducedMotion:'reduce'})
  const page=await context.newPage(),errors=[]
  page.on('pageerror',error=>errors.push(error.message))
  await page.goto(`${server.resolvedUrls.local[0]}portal.html?${new URLSearchParams({name,user,locale:'en'})}`)
  await page.waitForSelector('[data-ready]')
  // Two frames: the SDK fits the header in an animation frame after DSM's changes.
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))
  assert.deepEqual(errors,[])
  const h=await header(page)
  if(shots)await page.screenshot({path:join(shots,`${baseline?'before':'after'}-${label}.png`)})
  if(!baseline){
   assert.equal(h.text,name,`${label}: the full name stays the element's text`)
   assert.equal(await page.evaluate(()=>document.scrollingElement.scrollWidth>innerWidth),false,`${label}: no horizontal overflow`)
   if(expect==='mobile'){
    assert.equal(h.mobile,true,`${label}: mobile layout`)
    assert.equal(h.fit,null,`${label}: no desktop fit in the mobile layout`)
   }else if(expect==='fit'){
    assert.equal(h.fit,null,`${label}: a name that fits is left alone`)
    assert.equal(h.title,null)
    assert.equal(h.overflow,'visible')
    assert.equal(h.lines,1,`${label}: one line`)
    if(h.button!==null)assert.ok(h.left+h.ink+34<=h.button,`${label}: name ends before the account button`)
   }else{
    assert.equal(h.fit,expect==='cut'?'cut':'icon',label)
    // The icon keeps its padding; the text ends 12px before the account button.
    if(expect==='cut')assert.ok(h.right<=h.button-11.5,`${label}: name box (${h.right}) ends 12px before the account button (${h.button})`)
    assert.equal(h.overflow,'hidden')
    assert.equal(h.title,name,`${label}: the full name is the tooltip`)
    assert.ok(h.height<=20.5,`${label}: one line (${h.height}px)`)
    if(expect==='cut'){
     assert.equal(h.ellipsis,'ellipsis')
     assert.ok(h.visible>=24,`${label}: at least an icon's width of name (${h.visible}px)`)
    }else assert.equal(h.visible,0,`${label}: no stub of the name`)
    // The page and DSM's account change: the fit follows without a reload.
    await page.setViewportSize({width:1600,height:800})
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))
    const wide=await header(page)
    assert.equal(wide.fit,null,`${label} widened to 1600`)
    assert.equal(wide.title,null,`${label}: tooltip removed with the fit`)
    await page.evaluate(()=>{const b=document.querySelector('.sds-standalone-main-window-header__toolbar__btn .x-btn-text');b.textContent='a'})
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))
    assert.equal((await header(page)).fit,null,`${label}: a short account frees the name`)
    await page.setViewportSize({width,height:800})
   }
  }
  console.log(`portal name: ${label} ok${baseline?' (baseline)':''}`)
  await context.close()
 }
}finally{await browser.close();await server.close()}
