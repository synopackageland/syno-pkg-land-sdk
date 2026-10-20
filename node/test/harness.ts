import { PassThrough } from 'node:stream'
import { createInterface } from 'node:readline'
import type { App } from '../src/index.js'

export const callContext = { user: 'alice', admin: false, locale: 'en', request_id: '0123456789abcdef0123456789abcdef', deadline_ms: 1000 }

// The runtime side of one channel, over two streams.
export function runtime(app: App) {
  const toApp = new PassThrough()
  const fromApp = new PassThrough()
  const lines = createInterface({ input: fromApp })[Symbol.asyncIterator]()
  const served = app.serve(toApp, fromApp).then(() => fromApp.end())
  return {
    served,
    send: (frame: unknown) => toApp.write(JSON.stringify(frame) + '\n'),
    close: () => toApp.end(),
    // The App's next frame, or null once it wrote its last.
    async recv(): Promise<any> {
      const { value, done } = await lines.next()
      return done ? null : JSON.parse(value)
    },
    async hello(versions = [1]) {
      this.send({ t: 'hello', versions })
      return this.recv()
    },
    async call(method: string, params: unknown = null, ctx: object = callContext) {
      const hello = await this.hello()
      this.send({ t: 'call', id: '1', method, params, ctx })
      return hello
    },
    // Calls method and returns the result frame.
    async result(method: string, params: unknown = null) {
      await this.call(method, params)
      return this.recv()
    },
  }
}
