import { createInterface } from 'node:readline';
// One channel to the runtime: a frame is one line of JSON (spec/bridge/v1
// README, Transport).
export class Channel {
    #lines;
    #output;
    constructor(input, output) {
        this.#lines = createInterface({ input, crlfDelay: Infinity })[Symbol.asyncIterator]();
        this.#output = output;
        // A runtime that went away is seen as the end of the channel, not a crash.
        output.on('error', () => { });
    }
    // The next frame, or null once the channel ended or broke.
    async recv() {
        try {
            const { value, done } = await this.#lines.next();
            if (done)
                return null;
            const frame = JSON.parse(value);
            return isReceived(frame) ? frame : null;
        }
        catch {
            return null;
        }
    }
    send(frame) {
        return this.write(JSON.stringify(frame));
    }
    // Writes an encoded frame; resolves once it is handed to the system, so
    // that exiting after it does not drop it.
    write(line) {
        return new Promise((resolve) => this.#output.write(line + '\n', () => resolve()));
    }
}
function isReceived(frame) {
    return typeof frame === 'object' && frame !== null && typeof frame.t === 'string';
}
