import { createInterface } from 'node:readline'
import type { Readable, Writable } from 'node:stream'
import type { Frame } from './frames.js'

// A frame as it arrives: a JSON object with a string t. Its other members are
// checked where they are read.
export type Received = { t: string; [member: string]: unknown }

// One channel to the runtime: a frame is one line of JSON (spec/bridge/v1
// README, Transport).
export class Channel {
  readonly #lines: AsyncIterator<string>
  readonly #output: Writable

  constructor(input: Readable, output: Writable) {
    this.#lines = createInterface({ input, crlfDelay: Infinity })[Symbol.asyncIterator]()
    this.#output = output
    // A runtime that went away is seen as the end of the channel, not a crash.
    output.on('error', () => {})
  }

  // The next frame, or null once the channel ended or broke.
  async recv(): Promise<Received | null> {
    try {
      const { value, done } = await this.#lines.next()
      if (done) return null
      const frame: unknown = JSON.parse(value)
      return isReceived(frame) ? frame : null
    } catch {
      return null
    }
  }

  send(frame: Frame): Promise<void> {
    return this.write(JSON.stringify(frame))
  }

  // Writes an encoded frame; resolves once it is handed to the system, so
  // that exiting after it does not drop it.
  write(line: string): Promise<void> {
    return new Promise((resolve) => this.#output.write(line + '\n', () => resolve()))
  }
}

function isReceived(frame: unknown): frame is Received {
  return typeof frame === 'object' && frame !== null && typeof (frame as { t?: unknown }).t === 'string'
}
