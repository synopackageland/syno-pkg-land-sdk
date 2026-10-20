/** Shared host flow. CGI plans are never accepted as completed authorization. */
export const AUTHORIZATION_RUNTIME = `function createAuthorizationAccess(options) {
  var busy = null;
  function fail(code) { var error = new Error(code); error.code = code; return error; }
  function status() { return options.call("authorization.status").then(function (d) {
    if (!d || d.command !== "authorization" || d.schema_version !== 1 || ["ready","authorization_required","not_admin","session_expired","unavailable"].indexOf(d.state) < 0) throw fail("authorization_bad_response");
    // upgrade_reapproval is a reason-only tombstone: it can only explain a pending request.
    if (d.reason === "upgrade_reapproval" && d.state !== "authorization_required") throw fail("authorization_bad_response");
    return d;
  }); }
  function plan(method) { return options.call(method).then(function (p) {
    if (!p || p.command !== "authorization" || p.schema_version !== 1 || p.phase !== "plan" || !Array.isArray(p.operations) || p.operations.length === 0 || p.operations.length > options.tasks.length) throw fail("authorization_bad_response");
    var seen = {};
    p.operations.forEach(function (op) { if (seen[op] || !options.tasks.some(function (t) { return t.op === op; })) throw fail("authorization_bad_response"); seen[op] = true; });
    return p;
  }); }
  // The backend's grant decides: a task still waiting at the window's limit, or
  // one signalled after it wrote the grant, has still authorized the package.
  function settled(e) {
    if (!e || (e.code !== "root_timeout" && e.code !== "root_task_failed")) throw e;
    return status().then(function (d) { if (d.state !== "ready") throw e; });
  }
  function exclusive(fn) { if (busy) return busy; busy = Promise.resolve().then(fn).then(function (s) { busy = null; return s; },function (e) { busy = null; throw e; });return busy; }
  function authorize(ops,run) { return ops.reduce(function (previous,op) { return previous.then(function () {
    return options.root.authorize(op).then(function (state) { if (state !== "ready") throw fail("root_cancelled"); return run ? options.root.run(op).catch(settled) : null; });
  }); },Promise.resolve()); }
  return {canManage:function () {return !!options.user && options.user().admin === true;},status:status,request:function () { return exclusive(function () {
    return plan("authorization.request").then(function (p) { return authorize(p.operations,p.run === true); }).then(status,function (e) { if (e.code === "root_cancelled") return status();throw e; });
  }); },revoke:function () { return exclusive(function () {
    return plan("authorization.revoke").then(function (p) {
      if (p.withdraw === true && typeof options.withdraw !== "function") throw fail("authorization_bad_response");
      var disabled = p.withdraw === true ? options.withdraw() : p.disable === true ? authorize(["helper_disable"],true) : Promise.resolve();
      return disabled.then(function () { return p.purge === true ? authorize(["helper_purge"],true) : null; }).then(function () { return p.operations.reduce(function (previous,op) {return previous.then(function () {return options.root.remove(op);});},Promise.resolve()); });
    }).then(status,function (e) { if (e.code === "root_cancelled") return status();throw e; });
  }); }};
}`;
