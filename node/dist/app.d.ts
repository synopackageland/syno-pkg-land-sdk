import { Writable, type Readable } from 'node:stream';
import { type Context } from './context.js';
/** The channel protocol version this SDK speaks. */
export declare const VERSION = 1;
/** The SDK's name in hello: node/<package version>. */
export declare const SDK = "node/0.37.0";
/**
 * A method's implementation. params is the request's params as the browser
 * sent them, not checked against P. The result is the data the browser gets,
 * any JSON value; undefined is null.
 */
export type Handler<P = unknown> = (ctx: Context, params: P) => unknown;
export interface AppOptions {
    /**
     * Where an unexpected error goes (stderr, the DSM log, by default); the
     * browser only sees internal.
     */
    log?: (error: unknown) => void;
}
/** The App: its methods, served on one channel per call. */
export declare class App {
    #private;
    constructor(options?: AppOptions);
    /** Registers a method; its name must be in the package's bridge.json. */
    method<P = unknown>(name: string, handler: Handler<P>): this;
    /**
     * Answers the one call on a channel; resolves once the result is written,
     * or the channel ended without a call.
     */
    serve(input: Readable, output: Writable): Promise<void>;
    /** What --describe prints: the App's methods, for the package build. */
    describe(): {
        sdk: string;
        versions: number[];
        methods: string[];
    };
    /**
     * Answers the one call the runtime sends on stdin and exits. Refuses to
     * start as root (exit status 1) or outside the runtime, when
     * SYNOPKGLAND_BRIDGE is not stdio (exit status 2). With --describe it
     * prints describe() as one JSON line instead. While it serves, what the App
     * writes to process.stdout (console.log) goes to stderr.
     */
    run(): Promise<void>;
}
