// Real-browser layout check for AppShell's default page (no visible title,
// only the content scrolls). For each width: the document never scrolls and
// nothing scrolls sideways; a plain page scrolls in one container under a page
// bar that stays; a settings page (in tabs or not, inside the App's wrappers)
// scrolls only its SettingsPage body, with the tab strip at the top and the
// footer on the bottom edge. --shots <dir> saves top and bottom screenshots.
// playwright-core is not a dependency: npm i --no-save playwright-core@1.58.2 first.
import {createServer} from 'vite'
import {fileURLToPath} from 'node:url'
import {mkdirSync} from 'node:fs'
import {join} from 'node:path'

const {chromium}=await import('playwright-core').catch(()=>{console.error('shell layout: run npm i --no-save playwright-core@1.58.2 first');process.exit(2)})
const here=fileURLToPath(new URL('.',import.meta.url))
const arg=name=>{const at=process.argv.indexOf(name);return at>0?process.argv[at+1]:undefined}
const shots=arg('--shots'),tag=arg('--tag')??''
if(shots)mkdirSync(shots,{recursive:true})
const server=await createServer({configFile:join(here,'vite.config.ts')})
await server.listen()
const base=server.resolvedUrls.local[0]
const browser=await chromium.launch(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{})
const failures=[]
const fail=msg=>{failures.push(msg);console.error('FAIL',msg)}

// Every element that scrolls, and whether the document or anything sideways does.
const scrollers=page=>page.evaluate(()=>{
 const out=[]
 for(const e of document.querySelectorAll('*')){
  // A closed phone drawer is out of view and out of reach.
  if(e.closest('[aria-hidden="true"],[inert]'))continue
  const s=getComputedStyle(e)
  if(['auto','scroll'].includes(s.overflowY)&&e.scrollHeight>e.clientHeight+1)out.push(e.matches('[data-ui-layout-main] > main')?'layout-main':e.className?.toString?.()||e.tagName)
  if(['auto','scroll'].includes(s.overflowX)&&e.scrollWidth>e.clientWidth+1)out.push(`x:${e.className?.toString?.()||e.tagName}`)
 }
 const d=document.scrollingElement
 if(d.scrollHeight>innerHeight+1)out.push('document')
 if(d.scrollWidth>innerWidth)out.push('x:document')
 return out
})
const box=(page,selector)=>page.evaluate(s=>{const e=[...document.querySelectorAll(s)].find(e=>e.getClientRects().length);if(!e)return null;const b=e.getBoundingClientRect();return {top:Math.round(b.top),bottom:Math.round(b.bottom),left:Math.round(b.left),right:Math.round(b.right)}},selector)
const toBottom=(page,selector)=>page.evaluate(s=>{const e=[...document.querySelectorAll(s)].find(e=>e.getClientRects().length);e.scrollTop=e.scrollHeight},selector)

for(const [width,height] of [[1280,800],[390,844]]){
 const page=await browser.newPage({viewport:{width,height},deviceScaleFactor:width<640?2:1})
 page.on('pageerror',e=>fail(`${width}: page error ${e.message}`))
 const open=async(id)=>{await page.goto(`${base}shell.html?page=${id}`);await page.waitForSelector('.app-page-content > *');await page.waitForTimeout(400)}
 const shot=async name=>{if(shots)await page.screenshot({path:join(shots,`${tag}${width}-${name}.png`)})}

 // A plain long page: one scroller (.app-page), a page bar that stays.
 await open('home')
 const title=await page.evaluate(()=>document.querySelector('h1.app-page-title')?.textContent)
 if(title!=='Home')fail(`${width} home: hidden title ${title}`)
 if(await page.$('[data-ui-page-header]'))fail(`${width} home: a visible page header`)
 // On phones the UI moves page actions into its native top bar.
 const barSelector=width<640?'[data-ui-mobile-navigation]':'[data-app-page-bar]'
 const actionsSelector=width<640?'[data-ui-mobile-page-actions] button':'[data-app-page-bar] button'
 const bar=await box(page,barSelector)
 if(!bar)fail(`${width} home: no page bar`)
 const actions=await page.evaluate(selector=>[...document.querySelectorAll(selector)].map(b=>b.textContent.trim()),actionsSelector)
 if(actions.join()!=='Rescan,Refresh')fail(`${width} home: bar actions ${actions}`)
 const home=await scrollers(page)
 if(home.length!==1||!home[0].includes('app-page'))fail(`${width} home: scrollers ${home}`)
 await shot('home-top')
 await toBottom(page,'.app-page')
 await page.waitForTimeout(150)
 const barAfter=await box(page,barSelector)
 if(JSON.stringify(bar)!==JSON.stringify(barAfter))fail(`${width} home: bar moved ${JSON.stringify({bar,barAfter})}`)
 const scroller=await box(page,'.app-page')
 if(bar&&scroller.top<bar.bottom)fail(`${width} home: scroller starts under the bar`)
 await shot('home-bottom')

 // Desktop settings scroll their body; on phones the page scrolls as one
 // below the native bar. Both keep the footer at the bottom edge.
 for(const id of ['settings','plain']){
  await open(id)
  // Mobile SectionTabs starts with its section list; opening a section
  // mounts the settings body and footer in the push panel.
  if(id==='settings'&&width<640){
   await page.click('[data-ui-tab-value=general]');await page.waitForTimeout(400)
  }
  const list=await scrollers(page)
  const pageScroll=width<640
  if(pageScroll&&!await page.$('[data-ui-layout-scroll="page"]'))fail(`${width} ${id}: missing phone page-scroll contract`)
  if(list.length!==1||!list[0].includes(pageScroll?'layout-main':'ui-settings-page-body'))fail(`${width} ${id}: scrollers ${list}`)
  const strip=id==='settings'?await box(page,'nav.ui-tab-strip'):null
  const footer=await box(page,'[data-ui-settings-page-footer]')
  const main=await box(page,'[data-ui-layout-main]')
  if(!footer)fail(`${width} ${id}: no footer`)
  else if(Math.abs(footer.bottom-main.bottom)>1)fail(`${width} ${id}: footer bottom ${footer.bottom}, window ${main.bottom}`)
  if(footer&&footer.bottom>height)fail(`${width} ${id}: footer below the viewport`)
  await shot(`${id}-top`)
  await toBottom(page,pageScroll?'[data-ui-layout-main] > main':'[data-ui-settings-page-body]')
  await page.waitForTimeout(150)
  const footerAfter=await box(page,'[data-ui-settings-page-footer]')
  if(JSON.stringify(footer)!==JSON.stringify(footerAfter))fail(`${width} ${id}: footer moved`)
  if(strip&&JSON.stringify(strip)!==JSON.stringify(await box(page,'nav.ui-tab-strip')))fail(`${width} ${id}: tab strip moved`)
  await shot(`${id}-bottom`)
 }

 // A tab that is not a SettingsPage scrolls under the strip.
 await open('settings')
 await page.click('[data-ui-tab-value=about]');await page.waitForTimeout(400)
 const about=await scrollers(page)
 if(about.length!==1||!about[0].includes(width<640?'layout-main':'ui-tab-panel'))fail(`${width} about tab: scrollers ${about}`)
 await shot('about-tab')

 // header: true keeps the shell's PageHeader.
 await open('titled')
 if(!await page.$('[data-ui-page-header] h1'))fail(`${width} titled: no page header`)
 if(await box(page,'[data-app-page-bar]'))fail(`${width} titled: a page bar beside the header`)
 await shot('titled')
 await page.close()
}
await browser.close();await server.close()
if(failures.length){console.error(`${failures.length} failure(s)`);process.exit(1)}
console.log('shell layout: ok')
