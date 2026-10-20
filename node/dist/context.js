import { ServiceError } from './errors.js';
const opName = /^[a-z][a-z0-9_]*(\.[a-z][a-zA-Z0-9_]*)+$/;
// One call on its channel: the handler's Context and the services it waits for.
export class Call {
    ctx;
    #channel;
    #abort = new AbortController();
    #pending = new Map();
    #next = 0;
    #batchId;
    constructor(channel, c, batchId, startedAt) {
        this.#batchId = batchId;
        this.#channel = channel;
        const signal = this.#abort.signal;
        this.ctx = {
            user: c.user,
            admin: c.admin,
            locale: c.locale,
            requestId: c.request_id,
            deadline: (startedAt ?? Date.now()) + c.deadline_ms,
            deadlineMs: c.deadline_ms,
            signal,
            get cancelled() {
                return signal.aborted;
            },
            runtime: (op, params) => this.#runtime(op, params),
        };
    }
    #runtime(op, params = null) {
        if (!opName.test(op))
            return Promise.reject(new TypeError(`invalid service name: ${op}`));
        if (this.#abort.signal.aborted)
            return Promise.reject(closed(op));
        const id = `${this.#batchId === undefined ? "" : `b${this.#batchId}-`}s${++this.#next}`;
        return new Promise((resolve, reject) => {
            // send throws for params JSON cannot encode; the answer comes later.
            void this.#channel.send({ t: 'svc', id, op, params, ...(this.#batchId === undefined ? {} : { batch_id: this.#batchId }) });
            this.#pending.set(id, { op, resolve, reject });
        });
    }
    // Settles the service a svc_result answers.
    answer(frame) {
        const pending = this.#pending.get(frame.id);
        if (!pending)
            return;
        this.#pending.delete(frame.id);
        const error = frame.error;
        if (frame.ok === true)
            pending.resolve(frame.result ?? null);
        else
            pending.reject(new ServiceError(pending.op, error?.code ?? 'internal', error?.message ?? 'malformed service reply'));
    }
    // The request is over: what is pending ends, and nothing more is asked.
    cancel(reason) {
        this.#abort.abort(reason);
        for (const p of this.#pending.values())
            p.reject(closed(p.op));
        this.#pending.clear();
    }
    // The channel ended, or the result is written; a first reason stays.
    close() {
        this.cancel('canceled');
    }
}
function closed(op) {
    return new ServiceError(op, 'request_canceled', 'the runtime closed the channel');
}
