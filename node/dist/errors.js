import { protocolRefusals } from './refusals.js';
// The runtime's own refusals (spec/bridge/v1 README, Errors). An App that
// returned one would make the browser act on a decision the runtime did not
// take, such as signing in again.
export const reservedCodes = new Set([
    'session_required', 'admin_required', 'app_privilege_denied', 'auth_unavailable',
    'unknown_method', 'cross_origin', 'origin_required', 'request_timeout',
    'request_canceled', 'request_too_large', 'json_required', 'method_not_allowed',
    'app_unavailable',
]);
const appCode = /^[a-z][a-z0-9_]{0,63}$/;
const maxMessageBytes = 1024;
// isAppCode reports whether an App may return code: its own lower-case code,
// or a protocol v1 refusal it got from a service and passes on.
export function isAppCode(code) {
    return (appCode.test(code) && !reservedCodes.has(code)) || protocolRefusals.has(code);
}
/**
 * An error the browser sees as {code, message}. code is the App's own code
 * (^[a-z][a-z0-9_]{0,63}$, except the runtime's) or an upper-case refusal of
 * spec/protocol/v1/errors.json, which @synopackageland/sdk translates; any
 * other code throws TypeError. message is cut to 1024 bytes of UTF-8.
 */
export class AppError extends Error {
    code;
    constructor(code, message) {
        if (!isAppCode(code))
            throw new TypeError(`an App may not return error code ${JSON.stringify(code)}`);
        super(truncate(message));
        this.name = 'AppError';
        this.code = code;
    }
    /** AppError('bad_request', message). */
    static badRequest(message) {
        return new AppError('bad_request', message);
    }
}
function truncate(message) {
    if (Buffer.byteLength(message) <= maxMessageBytes)
        return message;
    let bytes = 0;
    let end = 0;
    for (const char of message) {
        bytes += Buffer.byteLength(char);
        if (bytes > maxMessageBytes)
            break;
        end += char.length;
    }
    return message.slice(0, end);
}
/**
 * A service the App asked for with ctx.runtime was refused. code is the
 * runtime's: an api code such as unknown_service, or for executor-backed
 * services a code of spec/protocol/v1/errors.json. Uncaught, an upper-case
 * protocol v1 refusal reaches the browser as an AppError would; any other
 * code is internal.
 */
export class ServiceError extends Error {
    code;
    /** The service: the op given to ctx.runtime. */
    op;
    constructor(op, code, message) {
        super(message);
        this.name = 'ServiceError';
        this.code = code;
        this.op = op;
    }
}
// The error the browser gets for an error the handler did not catch, or
// undefined when it gets internal.
export function browserError(e) {
    if (e instanceof AppError)
        return { code: e.code, message: e.message };
    if (e instanceof ServiceError && protocolRefusals.has(e.code))
        return { code: e.code, message: truncate(e.message) };
    return undefined;
}
