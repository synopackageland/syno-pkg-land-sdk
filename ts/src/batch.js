/**
 * Bind batching to the same guarded transport as single calls. Transport
 * failures reject the batch; per-method failures remain ordered results.
 * @param {(envelope: object, options?: {signal?: AbortSignal}) => Promise<unknown>} send
 */
export function createBatch(send) {
  /**
   * @param {ReadonlyArray<import("./client").BatchCall>} calls
   * @param {{signal?: AbortSignal}} [options]
   */
  return function batch(calls, options) {
    if (!Array.isArray(calls) || calls.length < 1 || calls.length > 8 || calls.some(function (call) {
      return !call || typeof call.method !== 'string' || !/^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$/.test(call.method) || Object.keys(call).some(function (key) { return key !== 'method' && key !== 'params'; });
    })) return Promise.reject({code: 'bad_request', message: 'Batch needs one to eight methods'});
    return send({v: 1, calls: calls}, options).then(function (value) {
      var reply = /** @type {{v?: unknown, results?: unknown}} */ (value);
      if (!reply || reply.v !== 1 || !Array.isArray(reply.results) || reply.results.length !== calls.length || reply.results.some(function (result) {
        return !result || typeof result.success !== 'boolean' || (!result.success && (!result.error || typeof result.error.code !== 'string' || typeof result.error.message !== 'string'));
      })) throw {code: 'bad_response', message: 'Invalid batch response'};
      return reply.results;
    });
  };
}
