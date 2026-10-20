import { test } from 'node:test'
import assert from 'node:assert/strict'
import { App, ServiceError, type Context } from '../src/index.js'
import { runtime } from './harness.js'

// A method that hands its Context to the test and waits for it.
function held() {
  let release!: (data: unknown) => void
  const done = new Promise((resolve) => (release = resolve))
  let ctx!: Context
  const app = new App({ log: () => {} })
  const entered = new Promise<void>((resolve) => {
    app.method('a.hold', (c) => {
      ctx = c
      resolve()
      return done
    })
  })
  return { app, entered, release, ctx: () => ctx }
}

test('ctx.runtime asks for a service and resolves to its result', async () => {
  const app = new App().method('a.save', async (ctx) => [await ctx.runtime('files.list', { path: 'x' }), await ctx.runtime('portal.status')])
  const rt = runtime(app)
  await rt.call('a.save')
  let svc = await rt.recv()
  assert.deepEqual(svc, { t: 'svc', id: 's1', op: 'files.list', params: { path: 'x' } })
  rt.send({ t: 'svc_result', id: 's1', ok: true, result: { items: [] } })
  svc = await rt.recv()
  assert.deepEqual(svc, { t: 'svc', id: 's2', op: 'portal.status', params: null })
  rt.send({ t: 'svc_result', id: 's2', ok: true })
  assert.deepEqual((await rt.recv()).data, [{ items: [] }, null])
})

test('answers may come in any order', async () => {
  const app = new App().method('a.both', (ctx) => Promise.all([ctx.runtime('a.one', 1), ctx.runtime('a.two', 2)]))
  const rt = runtime(app)
  await rt.call('a.both')
  const [first, second] = [await rt.recv(), await rt.recv()]
  rt.send({ t: 'svc_result', id: second.id, ok: true, result: 'two' })
  rt.send({ t: 'svc_result', id: first.id, ok: true, result: 'one' })
  assert.deepEqual((await rt.recv()).data, ['one', 'two'])
})

test('a refusal rejects with ServiceError', async () => {
  let caught: unknown
  const app = new App().method('a.read', (ctx) => ctx.runtime('files.read', {}).catch((e) => { caught = e; return 'handled' }))
  const rt = runtime(app)
  await rt.call('a.read')
  rt.send({ t: 'svc_result', id: (await rt.recv()).id, ok: false, error: { code: 'unknown_service', message: 'no such service' } })
  assert.equal((await rt.recv()).data, 'handled')
  assert.ok(caught instanceof ServiceError)
  assert.deepEqual([caught.code, caught.message, caught.op], ['unknown_service', 'no such service', 'files.read'])
})

test('an uncaught protocol refusal reaches the browser; other service errors are internal', async () => {
  for (const [code, want] of [['SCOPE_DENIED', 'SCOPE_DENIED'], ['unknown_service', 'internal'], ['session_required', 'internal'], ['needs-scope-confirmation', 'internal']]) {
    const logged: unknown[] = []
    const app = new App({ log: (e) => logged.push(e) }).method('a.read', (ctx) => ctx.runtime('files.read', {}))
    const rt = runtime(app)
    await rt.call('a.read')
    rt.send({ t: 'svc_result', id: (await rt.recv()).id, ok: false, error: { code, message: 'refused' } })
    const { error } = await rt.recv()
    assert.equal(error.code, want, code)
    assert.equal(logged.length, want === 'internal' ? 1 : 0, code)
  }
})

test('an invalid op or params reject without asking the runtime', async () => {
  const app = new App().method('a.bad', (ctx) => Promise.all([
    ctx.runtime('Files', {}).catch((e) => e instanceof TypeError),
    ctx.runtime('files.list', 1n).catch((e) => e instanceof TypeError),
  ]))
  const rt = runtime(app)
  assert.deepEqual(await rt.result('a.bad'), { t: 'result', id: '1', ok: true, data: [true, true] })
})

test('cancel aborts the signal with its reason', async () => {
  const h = held()
  const rt = runtime(h.app)
  await rt.call('a.hold')
  await h.entered
  const pending = h.ctx().runtime('files.list', {})
  await rt.recv()
  rt.send({ t: 'cancel', id: '2', reason: 'deadline' })
  rt.send({ t: 'cancel', id: '1', reason: 'deadline' })
  await new Promise((resolve) => h.ctx().signal.addEventListener('abort', resolve))
  assert.deepEqual([h.ctx().cancelled, h.ctx().signal.reason], [true, 'deadline'])
  await assert.rejects(pending, (e) => e instanceof ServiceError && e.code === 'request_canceled')
  // The request is over: services are refused without asking.
  await assert.rejects(h.ctx().runtime('files.list', {}), (e) => e instanceof ServiceError && e.code === 'request_canceled')
  h.release('stopped')
  assert.equal((await rt.recv()).data, 'stopped')
})

test('the end of the channel cancels and fails what is pending', async () => {
  const h = held()
  const rt = runtime(h.app)
  await rt.call('a.hold')
  await h.entered
  const pending = h.ctx().runtime('files.list', {})
  await rt.recv()
  rt.close()
  await assert.rejects(pending, (e) => e instanceof ServiceError && e.code === 'request_canceled')
  assert.deepEqual([h.ctx().cancelled, h.ctx().signal.reason], [true, 'canceled'])
  await assert.rejects(h.ctx().runtime('files.list', {}), ServiceError)
  h.release(null)
  await rt.served
})
