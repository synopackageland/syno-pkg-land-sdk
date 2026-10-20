import { test } from 'node:test'
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { createInterface } from 'node:readline'
import { fileURLToPath } from 'node:url'
import { SDK } from '../src/index.js'
import { callContext } from './harness.js'

const fixture = (name: string) => fileURLToPath(new URL(`fixtures/${name}`, import.meta.url))

function start(env: Record<string, string>, preload: string[] = [], args: string[] = []) {
  const child = spawn(process.execPath, [...preload, fixture('greet.js'), ...args], { env: { PATH: process.env.PATH ?? '', ...env } })
  let stderr = ''
  child.stderr.on('data', (b) => (stderr += b))
  const lines = createInterface({ input: child.stdout })[Symbol.asyncIterator]()
  return {
    child,
    stderr: () => stderr,
    send: (frame: unknown) => child.stdin.write(JSON.stringify(frame) + '\n'),
    recv: async () => JSON.parse((await lines.next()).value),
    exit: async () => (await once(child, 'exit'))[0],
  }
}

test('run answers the one call on stdin and exits; console.log goes to stderr', async () => {
  const app = start({ SYNOPKGLAND_BRIDGE: 'stdio' })
  app.send({ t: 'hello', versions: [1] })
  assert.deepEqual((await app.recv()).methods, ['hello.greet'])
  app.send({ t: 'call', id: '7', method: 'hello.greet', params: null, ctx: callContext })
  assert.deepEqual(await app.recv(), { t: 'result', id: '7', ok: true, data: 'Hello, alice' })
  assert.equal(await app.exit(), 0)
  assert.match(app.stderr(), /greeting alice/)
})

test('--describe prints the methods for the package build', async () => {
  const app = start({}, [], ['--describe'])
  assert.deepEqual(await app.recv(), { sdk: SDK, versions: [1], methods: ['hello.greet'] })
  assert.equal(await app.exit(), 0)
})

test('run refuses to run as root', async () => {
  const app = start({ SYNOPKGLAND_BRIDGE: 'stdio' }, ['-r', fixture('root.cjs')])
  assert.equal(await app.exit(), 1)
  assert.match(app.stderr(), /refusing to run as root/)
})

test('run needs SYNOPKGLAND_BRIDGE=stdio', async () => {
  for (const env of [{}, { SYNOPKGLAND_BRIDGE: 'socket:/tmp/app.sock' }]) {
    const app = start(env)
    assert.equal(await app.exit(), 2)
    assert.match(app.stderr(), /SYNOPKGLAND_BRIDGE/)
  }
})
