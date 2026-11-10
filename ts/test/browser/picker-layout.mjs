// Real-browser layout check for the NAS folder picker (pnpm run test:browser).
// Loading must not move the dialog, the tree or the list; nothing scrolls
// sideways; the default size opens without a vertical scrollbar.
// --shots <dir> also saves loading, loaded and empty screenshots.
// playwright-core is not a dependency (registries behind a security policy
// may refuse it): npm i --no-save playwright-core@1.58.2 first.
import {createServer} from 'vite'
import {fileURLToPath} from 'node:url'
import {mkdirSync} from 'node:fs'
import {join} from 'node:path'

const {chromium}=await import('playwright-core').catch(()=>{console.error('picker layout: run pnpm add --save-dev playwright-core first');process.exit(2)})
const here=fileURLToPath(new URL('.',import.meta.url))
const shotsAt=process.argv.indexOf('--shots'),shots=shotsAt>0?process.argv[shotsAt+1]:undefined
const tag=process.argv.includes('--tag')?process.argv[process.argv.indexOf('--tag')+1]:''
if(shots)mkdirSync(shots,{recursive:true})
const server=await createServer({configFile:join(here,'vite.config.ts')})
await server.listen()
const base=server.resolvedUrls.local[0]
const browser=await chromium.launch(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{})
const failures=[]
const fail=(msg)=>{failures.push(msg);console.error('FAIL',msg)}

function boxes(page){return page.evaluate(()=>{
 const r=(s)=>{const e=document.querySelector(s);if(!e)return null;const b=e.getBoundingClientRect();return {x:Math.round(b.x*2)/2,y:Math.round(b.y*2)/2,w:Math.round(b.width*2)/2,h:Math.round(b.height*2)/2}}
 return {dialog:r('[role=dialog]'),tree:r('.file-tree'),list:r('.file-content'),columns:r('.file-columns')}
})}
// Elements inside the dialog that scroll, or would: sideways always, vertically
// only where the content is short enough to fit.
function overflow(page){return page.evaluate(()=>{
 const dialog=document.querySelector('[role=dialog]'),out=[]
 const db=dialog.getBoundingClientRect()
 for(const e of [dialog,...dialog.querySelectorAll('*')]){
  const s=getComputedStyle(e),name=e.className?.toString?.()||e.tagName
  if(['auto','scroll'].includes(s.overflowX)&&e.scrollWidth>e.clientWidth+1)out.push(`x-scroll ${name} ${e.scrollWidth}>${e.clientWidth}`)
  if(s.overflowX==='scroll'||s.overflowY==='scroll')out.push(`overflow:scroll ${name}`)
  if(['auto','scroll'].includes(s.overflowY)&&e.scrollHeight>e.clientHeight+1)out.push(`y-scroll ${name} ${e.scrollHeight}>${e.clientHeight}`)
  const b=e.getBoundingClientRect()
  if(b.width&&s.position!=='fixed'&&b.right>db.right+1&&!e.closest('[data-overflow-ok]'))out.push(`outside ${name} right ${b.right}>${db.right}`)
 }
 if(document.documentElement.scrollWidth>innerWidth)out.push(`page x-scroll ${document.documentElement.scrollWidth}>${innerWidth}`)
 return out
})}
const same=(a,b)=>JSON.stringify(a)===JSON.stringify(b)

for(const [width,height] of [[1280,800],[768,720]]){
 const page=await browser.newPage({viewport:{width,height},deviceScaleFactor:1})
 page.on('pageerror',e=>fail(`${width}: page error ${e.message}`))
 await page.goto(`${base}picker.html?hold`)
 await page.waitForSelector('[role=dialog] .file-columns')
 await page.waitForFunction(()=>window.picker.pending>0)
 await page.waitForTimeout(300)
 const loading=await boxes(page)
 if(shots)await page.screenshot({path:join(shots,`${tag}${width}-loading.png`)})
 await page.evaluate(()=>window.picker.hold(false))
 await page.waitForSelector('tr[data-row-key="/projects"]')
 await page.waitForTimeout(300)
 const loaded=await boxes(page)
 if(!same(loading,loaded))fail(`${width}: shares loading→loaded moved ${JSON.stringify({loading,loaded})}`)
 for(const o of await overflow(page))fail(`${width} shares: ${o}`)
 // Open a folder while its listing waits, then let it finish.
 await page.evaluate(()=>window.picker.hold(true))
 await page.dblclick('tr[data-row-key="/projects"]',{force:true})
 await page.waitForFunction(()=>window.picker.pending>0)
 await page.waitForTimeout(300)
 const folderLoading=await boxes(page)
 if(!same(folderLoading,loaded))fail(`${width}: folder loading moved ${JSON.stringify({loaded,folderLoading})}`)
 if(shots)await page.screenshot({path:join(shots,`${tag}${width}-folder-loading.png`)})
 await page.evaluate(()=>window.picker.hold(false))
 await page.waitForSelector('tr[data-row-key="/projects/design"]')
 await page.waitForTimeout(300)
 const folderLoaded=await boxes(page)
 if(!same(folderLoaded,loaded))fail(`${width}: folder loaded moved ${JSON.stringify({loaded,folderLoaded})}`)
 for(const o of await overflow(page))fail(`${width} folder: ${o}`)
 if(shots)await page.screenshot({path:join(shots,`${tag}${width}-loaded.png`)})
 await page.dblclick('tr[data-row-key="/projects/design"]')
 await page.waitForSelector('.file-empty')
 await page.waitForTimeout(300)
 const empty=await boxes(page)
 if(!same(empty,loaded))fail(`${width}: empty folder moved ${JSON.stringify({loaded,empty})}`)
 for(const o of await overflow(page))fail(`${width} empty: ${o}`)
 if(shots)await page.screenshot({path:join(shots,`${tag}${width}-empty.png`)})
 // Long folder names: cut in their cells, the breadcrumb and the footer, never wider than the dialog.
 const long='quarterly-planning-material-for-the-regional-distribution-team-2026-final-review'
 await page.evaluate(()=>window.picker.hold(false))
 await page.click(`[data-tree-node="/projects"]`)
 await page.waitForSelector(`tr[data-row-key="/projects/${long}"]`)
 await page.dblclick(`tr[data-row-key="/projects/${long}"]`)
 await page.waitForSelector(`tr[data-row-key="/projects/${long}/${long}"]`)
 await page.dblclick(`tr[data-row-key="/projects/${long}/${long}"]`)
 await page.waitForSelector('.file-empty')
 await page.waitForTimeout(300)
 const deep=await boxes(page)
 if(!same(deep,loaded))fail(`${width}: long path moved ${JSON.stringify({loaded,deep})}`)
 for(const o of await overflow(page))fail(`${width} long path: ${o}`)
 const title=await page.getAttribute('.nas-picker-selection','title',{timeout:2000}).catch(()=>undefined)
 if(title!==`/projects/${long}/${long}`)fail(`${width}: footer path tooltip ${title}`)
 if(shots)await page.screenshot({path:join(shots,`${tag}${width}-long-path.png`)})
 console.log(width,JSON.stringify(loaded))
 await page.close()
}
await browser.close()
await server.close()
if(failures.length){console.error(`${failures.length} failure(s)`);process.exit(1)}
console.log('picker layout: ok')
