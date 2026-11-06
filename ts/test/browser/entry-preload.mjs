// Run after the SDK build. A real modulepreload must fetch without evaluating.
import assert from 'node:assert/strict'
import {createServer} from 'node:http'
import {readFile} from 'node:fs/promises'
import {chromium} from 'playwright-core'

let pendingToken, pendingIdentity, tokenArrived, identityArrived, bundleRequests = 0
const server = createServer(async (request, response) => {
 const path = new URL(request.url, 'http://localhost').pathname
 if(path === '/webman/login.cgi') {pendingToken = response; tokenArrived(); return}
 if(path.endsWith('/cgi/api.cgi')) {pendingIdentity = response; identityArrived(); return}
 response.setHeader('Content-Type', path.endsWith('.js') ? 'text/javascript' : 'text/html')
 if(path.startsWith('/dist/') && !path.includes('..')) {
  try {response.end(await readFile(new URL(`../../${path.slice(1)}`, import.meta.url)))}
  catch {response.writeHead(404).end()}
 } else if(path === '/app.js') {
  bundleRequests++
  response.setHeader('Cache-Control', 'max-age=3600')
  response.end(`import '/dependency.js'; window.bundleExecuted = true; export function mount(root, ctx) {root.textContent = ctx.user.name; window.mounted = true}`)
 } else if(path === '/dependency.js') {
  response.end('window.dependencyExecuted = true')
 } else {
  response.end(`<div id="app"></div><script type="module">
   import {bootEntryPage} from '/dist/entry.js'
   window.bootDone = false
   bootEntryPage({packageId: 'ExampleApp', build: 'build-a', root: document.querySelector('#app'),
    appBundle: '/app.js?v=build-a', navigate: () => {window.navigated = true},
    mount: async (root, ctx) => (await import('/app.js?v=build-a')).mount(root, ctx),
   }).then(ctx => {window.ctx = ctx; window.bootDone = true})
  </script>`)
 }
})
await new Promise(resolve => server.listen(0, 'localhost', resolve))
const origin = `http://localhost:${server.address().port}`
const browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_PATH ? {executablePath: process.env.CHROMIUM_PATH} : {}), args: ['--no-sandbox']})
const reply = (response, body) => response.writeHead(200, {'Content-Type': 'application/json'}).end(JSON.stringify(body))
try {
 for(const outcome of ['success', 'token-failure', 'identity-failure']) {
  pendingToken = pendingIdentity = undefined; bundleRequests = 0
  const tokenArrival = new Promise(resolve => {tokenArrived = resolve})
  const identityArrival = new Promise(resolve => {identityArrived = resolve})
  const context = await browser.newContext(), page = await context.newPage(), errors = []
  page.on('pageerror', error => errors.push(error.message))
  const bundle = page.waitForResponse(response => response.url() === `${origin}/app.js?v=build-a`, {timeout: 5000})
  await page.goto(`${origin}/webman/3rdparty/ExampleApp/app/entry.html`)
  await (await bundle).finished()
  await tokenArrival
  assert.ok(pendingToken, 'token request is pending while the bundle has already downloaded')
  assert.equal(pendingIdentity, undefined)
  const execution = () => page.evaluate(() => [!!window.bundleExecuted, !!window.dependencyExecuted, !!window.mounted])
  assert.deepEqual(await execution(), [false, false, false], 'preload must not evaluate the App or dependencies')
  const identity = outcome !== 'token-failure' ? page.waitForRequest(request => request.url().endsWith('/cgi/api.cgi')) : undefined
  reply(pendingToken, outcome === 'token-failure' ? {success: false} : {success: true, SynoToken: 'test-token'})
  if(identity) {
   await identity
   await identityArrival
   assert.deepEqual(await execution(), [false, false, false], 'execution still waits for app.status identity')
   assert.ok(pendingIdentity)
   reply(pendingIdentity, {success: true, data: {user: outcome === 'success' ? 'alice' : '', admin: false}})
  }
  await page.waitForFunction(() => window.bootDone)
  assert.deepEqual(await execution(), outcome === 'success' ? [true, true, true] : [false, false, false])
  assert.equal(await page.evaluate(() => !!window.navigated), outcome !== 'success')
  assert.equal(bundleRequests, 1, 'mount reuses the preloaded module request')
  assert.deepEqual(errors, [])
  console.log(`entry preload: ${outcome}: bundle fetched before token/identity; execution order preserved`)
  await context.close()
 }
} finally {await browser.close(); server.closeAllConnections(); await new Promise(resolve => server.close(resolve))}
