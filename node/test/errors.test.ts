import { test } from 'node:test'
import assert from 'node:assert/strict'
import { App, AppError } from '../src/index.js'
import { runtime } from './harness.js'

const internal = { code: 'internal', message: 'internal error' }

function logged() {
  const errors: unknown[] = []
  return { errors, app: new App({ log: (e) => errors.push(e) }) }
}

test('an AppError reaches the browser', async () => {
  const rt = runtime(new App().method('a.fail', () => { throw new AppError('conflict', 'taken') }))
  assert.deepEqual(await rt.result('a.fail'), { t: 'result', id: '1', ok: false, error: { code: 'conflict', message: 'taken' } })
})

test('AppError.badRequest is bad_request', () => {
  const e = AppError.badRequest('note must be a string')
  assert.deepEqual([e instanceof AppError, e.code, e.message], [true, 'bad_request', 'note must be a string'])
})

test('an AppError may carry a protocol v1 refusal', async () => {
  const rt = runtime(new App().method('a.fail', () => { throw new AppError('SCOPE_DENIED', 'no') }))
  assert.deepEqual((await rt.result('a.fail')).error, { code: 'SCOPE_DENIED', message: 'no' })
})

test('AppError refuses codes an App may not return', () => {
  for (const code of ['session_required', 'app_unavailable', 'Conflict', 'CLIENT_X', 'needs-scope-confirmation', '', 'a'.repeat(65)]) {
    assert.throws(() => new AppError(code, 'm'), TypeError, code)
  }
  assert.equal(new AppError('a'.repeat(64), 'm').code, 'a'.repeat(64))
})

test('AppError keeps message within 1024 bytes of UTF-8', () => {
  const e = new AppError('too_long', 'é'.repeat(600))
  assert.equal(e.message, 'é'.repeat(512))
  assert.equal(new AppError('x', 'a'.repeat(1025)).message.length, 1024)
  assert.equal(new AppError('x', '😀'.repeat(300)).message, '😀'.repeat(256))
})

test('an unexpected error is internal and goes to the log', async () => {
  const { errors, app } = logged()
  app.method('a.crash', () => { throw new Error('secret detail') })
  const rt = runtime(app)
  assert.deepEqual((await rt.result('a.crash')).error, internal)
  assert.match(String(errors[0]), /secret detail/)
})

test('a rejected promise and a thrown non-Error are internal', async () => {
  for (const handler of [async () => { throw new Error('later') }, () => { throw 'text' }]) {
    const { errors, app } = logged()
    const rt = runtime(app.method('a.crash', handler))
    assert.deepEqual((await rt.result('a.crash')).error, internal)
    assert.equal(errors.length, 1)
  }
})

test('data that is not JSON is internal', async () => {
  const circular: Record<string, unknown> = {}
  circular.self = circular
  for (const data of [1n, circular]) {
    const { errors, app } = logged()
    const rt = runtime(app.method('a.bad', () => data))
    assert.deepEqual((await rt.result('a.bad')).error, internal)
    assert.equal(errors.length, 1)
  }
})

test('a method the App does not have is internal', async () => {
  const { errors, app } = logged()
  const rt = runtime(app.method('a.one', () => 1))
  assert.deepEqual((await rt.result('a.two')).error, internal)
  assert.match(String(errors[0]), /a\.two/)
})
