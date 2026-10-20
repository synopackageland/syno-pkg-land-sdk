export declare const reservedCodes: ReadonlySet<string>;
export declare function isAppCode(code: string): boolean;
/**
 * An error the browser sees as {code, message}. code is the App's own code
 * (^[a-z][a-z0-9_]{0,63}$, except the runtime's) or an upper-case refusal of
 * spec/protocol/v1/errors.json, which @synopackageland/sdk translates; any
 * other code throws TypeError. message is cut to 1024 bytes of UTF-8.
 */
export declare class AppError extends Error {
    readonly code: string;
    constructor(code: string, message: string);
    /** AppError('bad_request', message). */
    static badRequest(message: string): AppError;
}
/**
 * A service the App asked for with ctx.runtime was refused. code is the
 * runtime's: an api code such as unknown_service, or for executor-backed
 * services a code of spec/protocol/v1/errors.json. Uncaught, an upper-case
 * protocol v1 refusal reaches the browser as an AppError would; any other
 * code is internal.
 */
export declare class ServiceError extends Error {
    readonly code: string;
    /** The service: the op given to ctx.runtime. */
    readonly op: string;
    constructor(op: string, code: string, message: string);
}
export declare function browserError(e: unknown): {
    code: string;
    message: string;
} | undefined;
