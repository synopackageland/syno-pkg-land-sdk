import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { App, SDK } from '../src/index.js'
import { callContext, runtime } from './harness.js'

test('hello answers version 1 with the sorted methods', async () => {
  const app = new App().method('a.two', () => 2).method('a.one', () => 1)
  const rt = runtime(app)
  assert.deepEqual(await rt.hello([1, 2]), { t: 'hello', version: 1, sdk: SDK, methods: ['a.one', 'a.two'] })
})

test('describe lists the methods', () => {
  const app = new App().method('a.two', () => 2).method('a.one', () => 1)
  assert.deepEqual(app.describe(), { sdk: SDK, versions: [1], methods: ['a.one', 'a.two'] })
})

test('the sdk name carries the package version', () => {
  const pkg = JSON.parse(readFileSync(new URL('../../package.json', import.meta.url), 'utf8'))
  assert.equal(SDK, `node/${pkg.version}`)
})

test('a runtime without version 1 gets no answer', async () => {
  const rt = runtime(new App().method('a.one', () => 1))
  assert.equal(await rt.hello([2]), null)
  await rt.served
})

test('a first frame that is not hello gets no answer', async () => {
  const rt = runtime(new App().method('a.one', () => 1))
  rt.send({ t: 'call', id: '1', method: 'a.one', params: null, ctx: callContext })
  assert.equal(await rt.recv(), null)
})

test('invalid JSON ends the channel', async () => {
  const rt = runtime(new App().method('a.one', () => 1))
  await rt.hello()
  rt.send('x')
  rt.close()
  await rt.served
  assert.equal(await rt.recv(), null)
})

test('the handler gets the params and the context', async () => {
  let seen: unknown
  const app = new App().method('hello.greet', (ctx, params: { greeting: string }) => {
    seen = { user: ctx.user, admin: ctx.admin, locale: ctx.locale, requestId: ctx.requestId, deadlineMs: ctx.deadlineMs, cancelled: ctx.cancelled }
    assert.ok(Math.abs(ctx.deadline - (Date.now() + 1000)) < 500)
    return `${params.greeting}, ${ctx.user}`
  })
  const rt = runtime(app)
  assert.deepEqual(await rt.result('hello.greet', { greeting: 'Hi' }), { t: 'result', id: '1', ok: true, data: 'Hi, alice' })
  assert.deepEqual(seen, { user: 'alice', admin: false, locale: 'en', requestId: callContext.request_id, deadlineMs: 1000, cancelled: false })
  await rt.served
})

test('an async handler resolves to the data', async () => {
  const rt = runtime(new App().method('a.later', async () => ({ n: 1 })))
  assert.deepEqual((await rt.result('a.later')).data, { n: 1 })
})

test('undefined data is null', async () => {
  const rt = runtime(new App().method('a.none', () => undefined))
  assert.deepEqual(await rt.result('a.none'), { t: 'result', id: '1', ok: true, data: null })
})

test('method names follow the channel pattern and register once', () => {
  const app = new App().method('a.b', () => 1)
  assert.throws(() => app.method('a.b', () => 1), /registered twice: a\.b/)
  for (const name of ['a', 'A.b', 'a.', 'a.B', 'a-b.c', '1.a']) {
    assert.throws(() => app.method(name, () => 1), TypeError, name)
  }
})
