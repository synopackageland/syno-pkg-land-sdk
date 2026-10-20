import { SDK_VERSION } from './sdk-version.js'
import { Writable, type Readable } from 'node:stream'
import { Channel, type Received } from './channel.js'
import { Call, type Context } from './context.js'
import { browserError } from './errors.js'
import type { CallContext, Result } from './frames.js'

/** The channel protocol version this SDK speaks. */
export const VERSION = 1
/** The SDK's name in hello: node/<package version>. */
export const SDK = `node/${SDK_VERSION}`

const methodName = /^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$/

/**
 * A method's implementation. params is the request's params as the browser
 * sent them, not checked against P. The result is the data the browser gets,
 * any JSON value; undefined is null.
 */
export type Handler<P = unknown> = (ctx: Context, params: P) => unknown

export interface AppOptions {
  /**
   * Where an unexpected error goes (stderr, the DSM log, by default); the
   * browser only sees internal.
   */
  log?: (error: unknown) => void
}

const internal = { code: 'internal', message: 'internal error' }

/** The App: its methods, served on one channel per call. */
export class App {
  readonly #methods = new Map<string, Handler<never>>()
  readonly #log: (error: unknown) => void

  constructor(options: AppOptions = {}) {
    this.#log = options.log ?? ((error) => console.error(error))
  }

  /** Registers a method; its name must be in the package's bridge.json. */
  method<P = unknown>(name: string, handler: Handler<P>): this {
    if (!methodName.test(name)) throw new TypeError(`invalid method name: ${name}`)
    if (this.#methods.has(name)) throw new Error(`method registered twice: ${name}`)
    this.#methods.set(name, handler as Handler<never>)
    return this
  }

  /**
   * Answers the one call on a channel; resolves once the result is written,
   * or the channel ended without a call.
   */
  async serve(input: Readable, output: Writable): Promise<void> {
    const channel = new Channel(input, output)
    const hello = await channel.recv()
    if (hello?.t !== 'hello' || !Array.isArray(hello.versions) || !hello.versions.includes(VERSION)) return
    const batch = hello.batch === 1
    await channel.send({ t: 'hello', version: VERSION, sdk: SDK, methods: this.describe().methods, ...(batch ? {batch: 1} : {}) })
    const request = await channel.recv()
    if (!request) return
    if (batch && (request.t !== 'batch' || request.v !== 1 || !Array.isArray(request.calls) || request.calls.length < 1 || request.calls.length > 8)) return
    const calls = batch ? request.calls as Received[] : [request]
    const batchStartedAt = Date.now()
    const states = new Map<string, Call>()
    void (async () => {
      for (let f = await channel.recv(); f; f = await channel.recv()) {
        if (f.t === 'svc_result') for (const state of states.values()) state.answer(f)
        else if (f.t === 'cancel') for (const state of states.values()) state.cancel(f.reason === 'deadline' ? 'deadline' : 'canceled')
      }
      for (const state of states.values()) state.close()
    })()
    for (let i = 0; i < calls.length; i++) {
      const call = calls[i]!
      if (call.t !== 'call') return
      const id = call.id as string
      const state = new Call(channel, call.ctx as CallContext, batch ? i : undefined, batch ? batchStartedAt : undefined)
      states.set(id, state)
      const result = await this.#answer(id, call, state.ctx)
      state.close()
      await channel.write(result)
    }
  }

  /** What --describe prints: the App's methods, for the package build. */
  describe(): { sdk: string; versions: number[]; methods: string[] } {
    return { sdk: SDK, versions: [VERSION], methods: [...this.#methods.keys()].sort() }
  }

  /**
   * Answers the one call the runtime sends on stdin and exits. Refuses to
   * start as root (exit status 1) or outside the runtime, when
   * SYNOPKGLAND_BRIDGE is not stdio (exit status 2). With --describe it
   * prints describe() as one JSON line instead. While it serves, what the App
   * writes to process.stdout (console.log) goes to stderr.
   */
  async run(): Promise<void> {
    if (process.argv.length === 3 && process.argv[2] === '--describe') {
      process.stdout.write(JSON.stringify(this.describe()) + '\n')
      return
    }
    const refusal = refuse()
    if (refusal) {
      process.stderr.write(`@synopackageland/app: ${refusal.message}\n`)
      process.exitCode = refusal.status
      return
    }
    await this.serve(process.stdin, takeStdout())
    // The result is written; stdin and whatever the handler left behind
    // would keep the process alive.
    process.exit(0)
  }

  // The encoded result frame. Data that JSON cannot encode fails here, before
  // anything is written.
  async #answer(id: string, call: Received, ctx: Context): Promise<string> {
    const encode = (frame: Result) => JSON.stringify(frame)
    try {
      const handler = this.#methods.get(call.method as string)
      if (!handler) throw new Error(`the runtime called ${String(call.method)}, which the App does not have`)
      const data = await handler(ctx, call.params as never)
      return encode({ t: 'result', id, ok: true, data: data === undefined ? null : data })
    } catch (e) {
      const error = browserError(e)
      if (error) return encode({ t: 'result', id, ok: false, error })
      this.#log(e)
      return encode({ t: 'result', id, ok: false, error: internal })
    }
  }
}

function refuse(): { message: string; status: number } | undefined {
  if (process.geteuid?.() === 0) return { message: 'refusing to run as root', status: 1 }
  if (process.env.SYNOPKGLAND_BRIDGE !== 'stdio') {
    return { message: 'the runtime starts an App with SYNOPKGLAND_BRIDGE=stdio; run --describe to list its methods', status: 2 }
  }
  return undefined
}

// The channel's end of stdout. process.stdout itself, and so console.log,
// writes to stderr from now on: a stray line must not break the channel.
function takeStdout(): Writable {
  const stdout = process.stdout
  const write = stdout.write.bind(stdout)
  stdout.write = process.stderr.write.bind(process.stderr) as typeof stdout.write
  // A runtime that went away is seen as the end of the channel, not a crash.
  stdout.on('error', () => {})
  return new Writable({ write: (chunk, encoding, done) => write(chunk, encoding, done) })
}
