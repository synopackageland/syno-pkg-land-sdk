// The api golden fixture (api/golden_test.go goldenServer) as a Node App.
import { readFileSync, writeFileSync } from 'node:fs'
// node/dist is the built package: npm run build in node/.
import { App, AppError } from '../../node/dist/index.js'

const app = new App()
app.method('demo.echo', (ctx, p) => ({ params: p, user: ctx.user }))
app.method('demo.order', () => ({ zeta: '<a href="x">&</a>', alpha: '\u2028\u2029\t\x01é', empty: null }))
app.method('demo.nothing', () => null)
app.method('demo.admin', () => true)
app.method('demo.ping', () => 'pong')
app.method('demo.fail', () => { throw new AppError('conflict', 'a <b> & c') })

// The bridge golden fixture (bridge/golden_test.go bridgeGoApp): services
// and transfers.
const svc = async (ctx, p) => {
  try {
    return { result: await ctx.runtime(p.op, p.params) }
  } catch (e) {
    return { error: { code: e.code, message: e.message } }
  }
}
app.method('demo.svc', svc)
app.method('demo.svc_public', svc)
app.method('demo.upload', (ctx, p) => ({ query: p.query, size: p.upload.size, content: readFileSync(p.upload.path, 'utf8') }))
app.method('demo.fetch', (ctx, p) => {
  writeFileSync(p.download.path, 'hello ' + ctx.user)
  return { name: 'a b.txt', content_type: 'text/plain; charset=utf-8' }
})
await app.run()
