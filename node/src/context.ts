import type { Channel, Received } from './channel.js'
import { ServiceError } from './errors.js'
import type { CallContext, SvcError } from './frames.js'

/**
 * Why the request is over: its deadline passed, or the browser went away
 * (or the runtime closed the channel).
 */
export type CancelReason = 'deadline' | 'canceled'

/**
 * What the runtime verified about the call, and the way back to it. A
 * Context belongs to one call; do not keep it after the handler returns.
 */
export interface Context {
  /** The DSM user who called; empty for a public method. */
  readonly user: string
  /** Whether the user is a DSM administrator. */
  readonly admin: boolean
  /** The first Accept-Language tag of the request, or empty. */
  readonly locale: string
  /** The request's ID, 32 hex digits, also in the runtime's log. */
  readonly requestId: string
  /** When the runtime ends the request, in Date.now() milliseconds. */
  readonly deadline: number
  /** The time that was left when the runtime sent the call, in milliseconds. */
  readonly deadlineMs: number
  /**
   * Aborted when the request is over; its reason is a CancelReason. Pass it
   * to fetch, fs and timers to stop with the request.
   */
  readonly signal: AbortSignal
  /** Whether signal is aborted. */
  readonly cancelled: boolean
  /**
   * Asks the runtime for a service on behalf of the caller and resolves to
   * its result (null when it has none). Rejects with ServiceError when the
   * runtime refuses, with ServiceError request_canceled once the request is
   * over (pending calls too), and with TypeError for an op that is not a service name
   * or params JSON cannot encode.
   */
  runtime(op: string, params?: unknown): Promise<unknown>
}

const opName = /^[a-z][a-z0-9_]*(\.[a-z][a-zA-Z0-9_]*)+$/

type Pending = { op: string; resolve: (result: unknown) => void; reject: (error: Error) => void }

// One call on its channel: the handler's Context and the services it waits for.
export class Call {
  readonly ctx: Context
  readonly #channel: Channel
  readonly #abort = new AbortController()
  readonly #pending = new Map<string, Pending>()
  #next = 0
  readonly #batchId: number | undefined

  constructor(channel: Channel, c: CallContext, batchId?: number, startedAt?: number) {
    this.#batchId = batchId
    this.#channel = channel
    const signal = this.#abort.signal
    this.ctx = {
      user: c.user,
      admin: c.admin,
      locale: c.locale,
      requestId: c.request_id,
      deadline: (startedAt ?? Date.now()) + c.deadline_ms,
      deadlineMs: c.deadline_ms,
      signal,
      get cancelled() {
        return signal.aborted
      },
      runtime: (op, params) => this.#runtime(op, params),
    }
  }

  #runtime(op: string, params: unknown = null): Promise<unknown> {
    if (!opName.test(op)) return Promise.reject(new TypeError(`invalid service name: ${op}`))
    if (this.#abort.signal.aborted) return Promise.reject(closed(op))
    const id = `${this.#batchId === undefined ? "" : `b${this.#batchId}-`}s${++this.#next}`
    return new Promise((resolve, reject) => {
      // send throws for params JSON cannot encode; the answer comes later.
      void this.#channel.send({ t: 'svc', id, op, params, ...(this.#batchId === undefined ? {} : {batch_id: this.#batchId}) })
      this.#pending.set(id, { op, resolve, reject })
    })
  }

  // Settles the service a svc_result answers.
  answer(frame: Received): void {
    const pending = this.#pending.get(frame.id as string)
    if (!pending) return
    this.#pending.delete(frame.id as string)
    const error = frame.error as SvcError | undefined
    if (frame.ok === true) pending.resolve(frame.result ?? null)
    else pending.reject(new ServiceError(pending.op, error?.code ?? 'internal', error?.message ?? 'malformed service reply'))
  }

  // The request is over: what is pending ends, and nothing more is asked.
  cancel(reason: CancelReason): void {
    this.#abort.abort(reason)
    for (const p of this.#pending.values()) p.reject(closed(p.op))
    this.#pending.clear()
  }

  // The channel ended, or the result is written; a first reason stays.
  close(): void {
    this.cancel('canceled')
  }
}

function closed(op: string): ServiceError {
  return new ServiceError(op, 'request_canceled', 'the runtime closed the channel')
}
