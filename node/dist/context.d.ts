import type { Channel, Received } from './channel.js';
import type { CallContext } from './frames.js';
/**
 * Why the request is over: its deadline passed, or the browser went away
 * (or the runtime closed the channel).
 */
export type CancelReason = 'deadline' | 'canceled';
/**
 * What the runtime verified about the call, and the way back to it. A
 * Context belongs to one call; do not keep it after the handler returns.
 */
export interface Context {
    /** The DSM user who called; empty for a public method. */
    readonly user: string;
    /** Whether the user is a DSM administrator. */
    readonly admin: boolean;
    /** The first Accept-Language tag of the request, or empty. */
    readonly locale: string;
    /** The request's ID, 32 hex digits, also in the runtime's log. */
    readonly requestId: string;
    /** When the runtime ends the request, in Date.now() milliseconds. */
    readonly deadline: number;
    /** The time that was left when the runtime sent the call, in milliseconds. */
    readonly deadlineMs: number;
    /**
     * Aborted when the request is over; its reason is a CancelReason. Pass it
     * to fetch, fs and timers to stop with the request.
     */
    readonly signal: AbortSignal;
    /** Whether signal is aborted. */
    readonly cancelled: boolean;
    /**
     * Asks the runtime for a service on behalf of the caller and resolves to
     * its result (null when it has none). Rejects with ServiceError when the
     * runtime refuses, with ServiceError request_canceled once the request is
     * over (pending calls too), and with TypeError for an op that is not a service name
     * or params JSON cannot encode.
     */
    runtime(op: string, params?: unknown): Promise<unknown>;
}
export declare class Call {
    #private;
    readonly ctx: Context;
    constructor(channel: Channel, c: CallContext, batchId?: number, startedAt?: number);
    answer(frame: Received): void;
    cancel(reason: CancelReason): void;
    close(): void;
}
