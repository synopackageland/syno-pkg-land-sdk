// Real-browser check for AppShell and the UI's hidden-notices button (UI 0.20.1+).
// A page with no buttons has no page bar; dismissing its callout shows the bar
// with one button, and Show again removes it and hides the bar. A page with a
// PageInfo lists the dismissed callout in that button, with no second one.
// --shots <dir> saves light-theme screenshots of each step.
// playwright-core is not a dependency: npm i --no-save playwright-core@1.58.2 first.
import {createServer} from 'vite'
import {fileURLToPath} from 'node:url'
import {mkdirSync} from 'node:fs'
import {join} from 'node:path'

const {chromium}=await import('playwright-core').catch(()=>{console.error('callout recall: run npm i --no-save playwright-core@1.58.2 first');process.exit(2)})
const here=fileURLToPath(new URL('.',import.meta.url))
const arg=name=>{const at=process.argv.indexOf(name);return at>0?process.argv[at+1]:undefined}
const shots=arg('--shots')
if(shots)mkdirSync(shots,{recursive:true})
const server=await createServer({configFile:join(here,'vite.config.ts')})
await server.listen()
const base=server.resolvedUrls.local[0]
const browser=await chromium.launch(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{})
const failures=[]
const fail=msg=>{failures.push(msg);console.error('FAIL',msg)}
const page=await browser.newPage({viewport:{width:1100,height:640},colorScheme:'light'})
page.on('pageerror',e=>fail(`page error ${e.message}`))
const shot=async name=>{if(shots)await page.screenshot({path:join(shots,`${name}.png`)})}
const barShown=()=>page.evaluate(()=>{const bar=document.querySelector('[data-app-page-bar]');return !!bar&&bar.getClientRects().length>0})
const barButtons=()=>page.$$eval('[data-app-page-bar] button',list=>list.map(b=>b.getAttribute('aria-label')??b.textContent.trim()))
const calloutShown=()=>page.evaluate(()=>[...document.querySelectorAll('[data-ui-callout-close]')].some(e=>e.getClientRects().length>0))
const open=async id=>{await page.goto(`${base}callout.html?page=${id}`);await page.waitForSelector('[data-ui-callout-close]');await page.waitForTimeout(300)}

await page.goto(base+'callout.html');await page.evaluate(()=>localStorage.clear())
await open('plain')
if(await barShown())fail('plain: a page bar before any callout is dismissed')
await shot('1-plain-callout')
await page.click('[data-ui-callout-close]');await page.waitForTimeout(300)
if(!await barShown())fail('plain: no page bar after dismissing')
const recall=await barButtons()
if(recall.join()!=='Hidden notices')fail(`plain: bar buttons ${recall}`)
if(await calloutShown())fail('plain: the callout still shows')
await shot('2-plain-dismissed')
await page.click('[data-ui-callout-recall-trigger]');await page.waitForSelector('[data-ui-callout-restore]');await page.waitForTimeout(200)
await shot('3-plain-hidden-notices')
await page.click('[data-ui-callout-restore]');await page.waitForTimeout(400)
if(await page.$('[data-ui-callout-recall-trigger]'))fail('plain: the button stays after Show again')
if(await barShown())fail('plain: the page bar stays after Show again')
if(!await calloutShown())fail('plain: the callout did not come back')
await shot('4-plain-restored')

await open('notes')
await page.click('[data-ui-callout-close]');await page.waitForTimeout(300)
const notes=await barButtons()
if(notes.length!==1||!notes[0].startsWith('Notes'))fail(`notes: bar buttons ${notes}`)
await page.click('[data-app-page-bar] button[aria-label^="Notes"]');await page.waitForSelector('[data-ui-callout-restore]');await page.waitForTimeout(200)
await shot('5-notes-list')

await browser.close();await server.close()
if(failures.length){console.error(`${failures.length} failure(s)`);process.exit(1)}
console.log('callout recall: ok')
