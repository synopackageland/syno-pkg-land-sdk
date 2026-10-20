/**
 * Session carriers for a DSM desktop window or an Application Portal page.
 * This plain JavaScript factory is also embedded by the CLI into DSM's
 * generated window, so Go-only Apps need no frontend build toolchain.
 * @param {{read: () => string | undefined, write?: (token: string) => void, fetch: typeof globalThis.fetch}} options
 * @returns {{token: () => string | undefined, ready: () => Promise<string | undefined>}}
 */
export function createDsmSession(options) {
  /** @type {string | undefined} */
  var recovered;
  /** @type {Promise<string | undefined> | undefined} */
  var pending;
  /** @param {unknown} value @returns {string | undefined} */
  function valid(value) {
    return typeof value === 'string' && value.length > 0 && value.length <= 4096 && !/[\u0000-\u001f\u007f-\u009f]/.test(value) ? value : undefined;
  }
  function token() { return valid(options.read()) || recovered; }
  function ready() {
    var current = token();
    if (current) return Promise.resolve(current);
    // One attempt per page instance, shared by all concurrent dispatches.
    // Failure leaves the server's credential_not_ready response authoritative.
    if (pending) return pending;
    pending = new Promise(function (resolve) {
      var controller = new AbortController(), done = false;
      /** @param {string | undefined} value */
      function finish(value) {
        if (done) return;
        done = true;
        clearTimeout(timer);
        resolve(value);
      }
      var timer = setTimeout(function () { controller.abort(); finish(undefined); }, 15000);
      // Relative, like DSM's own WebAPI paths: portal aliases route only
      // their own prefix. Same-origin mode and redirect:error forbid a
      // redirect to another origin; no credentials enter a URL or storage.
      try {
        options.fetch('webman/login.cgi?enable_syno_token=yes', {
          method: 'GET', credentials: 'same-origin', mode: 'same-origin',
          redirect: 'error', cache: 'no-store', signal: controller.signal
        }).then(function (response) {
          return response.ok ? response.json() : undefined;
        }).then(function (reply) {
          if (done) return;
          // DSM may have supplied a newer token while the request ran.
          var current = valid(options.read());
          if (current) { finish(current); return; }
          var value = reply && reply.success === true ? valid(reply.SynoToken) : undefined;
          if (value) {
            if (options.write) options.write(value);
            recovered = value;
          }
          finish(value);
        }).catch(function () { finish(undefined); });
      } catch (_) { finish(undefined); }
    });
    return pending;
  }
  return {token: token, ready: ready};
}
