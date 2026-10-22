// Node SDK for Synology Package Land Apps (channel spec/bridge/v1).
//
// The Go runtime checks the request's origin, the DSM session, the entry
// gate, the route policy and the deadline; a handler here only sees calls
// that passed them, and asks the runtime for services with ctx.runtime. No
// dependencies; Node 22 or later.
export { App, SDK, VERSION, type AppOptions, type Handler } from './app.js'
export type { CancelReason, Context } from './context.js'
export { AppError, ServiceError } from './errors.js'
