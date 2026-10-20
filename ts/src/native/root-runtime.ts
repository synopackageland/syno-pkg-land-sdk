import {versionUnsupported} from "./dsm-version";
/** Build-time adapter for explicitly approved native product tasks. */
/** What the window embeds per root operation: the task it may create, and how to recognize it. */
export interface RootTask {
  op: string;
  name: string;
  header: string;
  script: string;
  /** Whether the package backend serves `root.output` for this op; helper ops report through the helper instead. */
  output: boolean;
}

/**
 * `createRootAccess(options)` → `{ state, authorize, run, remove }`, plain
 * ES5 for DSM's desktop. Every DSM call goes through `options.send` (the
 * AppWindow's sendWebAPI) as the signed-in administrator:
 *
 * - authorize: DSM's own password confirmation, then one disabled root task
 *   with exactly the embedded script. The one-use token goes into that single
 *   create call and is never kept.
 * - run: re-reads the task and runs it only if it is still exactly ours (the
 *   first run after an authorize uses the task that authorize just read back), then
 *   waits for its history entry and asks the backend for the output. A queued
 *   or running task keeps the wait going, up to RUN_LIMIT_MS (loaded NAS took
 *   4.5 minutes), and `options.progress({op, state, elapsedMs})` hears each poll. Repair
 *   additionally requires fresh PasswordConfirm and another exact task check.
 * - reads follow spec/scheduler/v1: a step lists Task Scheduler once and gets
 *   the task by that id; a step that deletes nothing gets the id an earlier
 *   list showed and lists again when DSM answers 4801 or another task. A
 *   duplicate name and any difference from the exact definition and script
 *   are never ready.
 * - an older version of our task is "outdated" and never runs; authorizing
 *   again replaces it. A same-named task that is not ours is a "conflict" and
 *   is never run, replaced or deleted.
 */
export const ROOT_RUNTIME = `function createRootAccess(options) {
  var API = "SYNO.Core.TaskScheduler";
  var POLL_MS = 500, MAX_POLL_MS = 1000, RUN_LIMIT_MS = 10 * 60 * 1000;
  // The task an authorize just read back as exactly ours, for the run that follows.
  var byOp = {}, running = {}, verified = {};
  // spec/scheduler/v1: the id each task name was last listed with.
  var listed = {};
  var legacy = null, probe = null;
  var versionUnsupported = ${versionUnsupported.toString()};
  options.tasks.forEach(function (task) { byOp[task.op] = task; });

  function now() { return options.now ? options.now() : Date.now(); }
  function report(task, state, started) {
    if (typeof options.progress !== "function") return;
    try { options.progress({ op: task.op, state: state, elapsedMs: now() - started }); } catch (error) {}
  }
  function pause(interval, deadline) {
    return options.wait(Math.max(0, Math.min(interval, deadline - now())));
  }
  function fail(code) {
    var error = new Error(code);
    error.code = code;
    return error;
  }
  function begin(op) {
    if (!Object.prototype.hasOwnProperty.call(byOp, op)) return Promise.reject(fail("root_unknown_op"));
    if (options.user().admin !== true) return Promise.reject(fail("admin_required"));
    return Promise.resolve(byOp[op]);
  }
  function readOutput(task) {
    return task.output ? options.call("root.output", { op: task.op }) : Promise.resolve(null);
  }
  function listTasks() {
    if (legacy === false) return options.send(API, "list", 3, { start: 0, limit: 500 });
    if (legacy === true) return legacyList();
    if (!probe) {
      probe = options.send(API, "list", 3, { start: 0, limit: 500 }).then(function (data) {
        legacy = false;
        return data;
      }, function (error) {
        if (!error || !versionUnsupported(error.code)) {
          probe = null;
          throw error;
        }
        legacy = true;
        return legacyList();
      });
    }
    return probe;
  }
  function legacyList() {
    return options.send(API, "list", 2, { offset: 0, limit: 500 }).then(function (data) {
      // Refuse a truncated snapshot: a same-named task could lie outside it.
      if (!data || !Array.isArray(data.tasks) || data.tasks.length > 500
          || !(typeof data.total === "number" && isFinite(data.total) && Math.floor(data.total) === data.total) || data.total !== data.tasks.length) throw fail("root_bad_response");
      return data;
    });
  }
  function readTask(ref) {
    return options.send(API, "get", legacy ? 3 : 4, ref);
  }
  function createTask(params) {
    return options.send(legacy ? API : API + ".Root", "create", legacy ? 3 : 4, params);
  }
  function deleteTask(ref) {
    return options.send(API, "delete", 2, { tasks: [ref] });
  }
  function runTask(ref) {
    return options.send(API, "run", 2, { tasks: [ref] });
  }
  // spec/scheduler/v1: Task Scheduler names repeat, so a list finds the id and
  // refuses a duplicate; get judges the task. cached: a step that deletes
  // nothing may get the id an earlier list showed, and lists again when that
  // get answers 4801 or another task.
  function locate(task, cached) {
    if (cached && listed[task.name] !== undefined) return Promise.resolve({ id: listed[task.name], real_owner: "root", cached: true });
    return listTasks().then(function (data) {
      if (!data || !Array.isArray(data.tasks)) throw fail("root_bad_response");
      var named = data.tasks.filter(function (summary) { return summary && summary.name === task.name; });
      delete listed[task.name];
      if (named.length === 0) return null;
      if (named.length > 1) return { duplicate: true };
      listed[task.name] = named[0].id;
      return { id: named[0].id, real_owner: named[0].real_owner };
    });
  }
  function find(task, cached) {
    return locate(task, cached).then(function (located) {
      if (!located) return { state: "missing" };
      if (located.duplicate) return { state: "conflict" };
      var ref = { id: located.id, real_owner: located.real_owner };
      return readTask(ref).then(function (detail) {
        if (located.cached && (!detail || String(detail.id) !== String(ref.id) || detail.name !== task.name)) return { relist: true };
        var script = detail && detail.extra && detail.extra.script;
        if (!detail || String(detail.id) !== String(ref.id) || detail.name !== task.name
            || detail.owner !== "root" || detail.real_owner !== "root" || detail.enable !== false || detail.type !== "script" || typeof script !== "string") {
          return { state: "conflict" };
        }
        if (script === task.script) return { state: "ready", ref: ref };
        if (script.indexOf(task.header) === 0) return { state: "outdated", ref: ref };
        return { state: "conflict" };
      }, function (error) {
        if (!located.cached || ["4801", "dsm_4801"].indexOf(String(error && error.code)) < 0) throw error;
        return { relist: true };
      }).then(function (found) {
        if (!found.relist) return found;
        delete listed[task.name];
        return find(task, false);
      });
    });
  }
  function definition(task, user) {
    var hours = [];
    for (var h = 1; h <= 23; h++) hours.push(h);
    return {
      name: task.name,
      real_owner: user,
      owner: "root",
      enable: false,
      type: "script",
      schedule: {
        date_type: 0, week_day: "0,1,2,3,4,5,6", repeat_date: 1001, monthly_week: [], hour: 0, minute: 0,
        repeat_hour: 0, repeat_min: 0, last_work_hour: 0,
        repeat_min_store_config: [1, 5, 10, 15, 20, 30], repeat_hour_store_config: hours
      },
      extra: { notify_enable: false, script: task.script, notify_mail: "", notify_if_error: false }
    };
  }
  function newest(history) {
    if (!Array.isArray(history)) throw fail("root_bad_response");
    return history.reduce(function (latest, entry) {
      return entry && (!latest || Number(entry.timestamp) > Number(latest.timestamp)) ? entry : latest;
    }, null);
  }
  function history(ref) {
    return options.send(API, "get_history_status_list", 1, { id: ref.id }).then(newest);
  }
  function waitForRun(task, ref, before, started, deadline, interval) {
    return history(ref).then(function (latest) {
      var fresh = latest && (!before || latest.timestamp !== before.timestamp);
      if (fresh && latest.exit_type !== "running" && latest.stop_time) {
        if (latest.exit_code !== 0) throw fail("root_task_failed");
        return;
      }
      report(task, fresh ? "running" : "queued", started);
      if (now() >= deadline) throw fail("root_timeout");
      return pause(interval, deadline).then(function () {
        return waitForRun(task, ref, before, started, deadline, Math.min(interval * 2, MAX_POLL_MS));
      });
    });
  }

  function validTime(value) {
    if (typeof value !== "string" || !/^\\d{4}-\\d{2}-\\d{2} \\d{2}:\\d{2}:\\d{2}$/.test(value)) return false;
    var normalized = value.replace(" ", "T") + "Z";
    var date = new Date(normalized);
    return !isNaN(date.getTime()) && date.toISOString().slice(0, 19) === normalized.slice(0, 19);
  }
  function bounded(call, deadline) {
    // A poll count alone cannot bound a stalled DSM callback. Discard late replies.
    return new Promise(function (resolve, reject) {
      var remaining = deadline - now();
      if (remaining <= 0) { reject(fail("root_timeout")); return; }
      var timer = setTimeout(function () { reject(fail("root_timeout")); }, remaining);
      Promise.resolve().then(call).then(function (value) {
        clearTimeout(timer);
        if (now() >= deadline) reject(fail("root_timeout"));
        else resolve(value);
      }, function (error) { clearTimeout(timer); reject(error); });
    });
  }
  function legacyHistory(ref, deadline) {
    return bounded(function () { return options.send(API, "view", 1, { id: ref.id }); }, deadline).then(function (records) {
      // DSM 7.1 omits data entirely when a task has never run.
      if (records === undefined) records = [];
      if (!Array.isArray(records) || records.length > 500) throw fail("root_bad_response");
      var seen = {};
      records.forEach(function (record) {
        if (!record || !validTime(record.start_time) || typeof record.stop_time !== "string"
            || (record.stop_time && (!validTime(record.stop_time) || record.stop_time < record.start_time))
            || typeof record.exit_type !== "string" || !(typeof record.exit_code === "number" && isFinite(record.exit_code) && Math.floor(record.exit_code) === record.exit_code)
            || seen[record.start_time]) throw fail("root_bad_response");
        seen[record.start_time] = true;
      });
      return records;
    });
  }
  function waitForLegacyRun(task, ref, before, started, deadline, interval) {
    return legacyHistory(ref, deadline).then(function (records) {
      var previous = before.map(function (record) { return record.start_time; });
      var newestBefore = previous.slice().sort().pop();
      var fresh = records.filter(function (record) { return previous.indexOf(record.start_time) === -1; });
      if (fresh.length > 1) throw fail("root_bad_response");
      var latest = fresh[0];
      if (latest && (!newestBefore || latest.start_time > newestBefore) && latest.stop_time) {
        if (latest.exit_code !== 0 || latest.exit_type !== "normal") throw fail("root_task_failed");
        return;
      }
      report(task, latest ? "running" : "queued", started);
      if (now() >= deadline) throw fail("root_timeout");
      return pause(interval, deadline).then(function () {
        return waitForLegacyRun(task, ref, before, started, deadline, Math.min(interval * 2, MAX_POLL_MS));
      });
    });
  }
  function execute(task, verified) {
    var started = now(), deadline = started + RUN_LIMIT_MS;
    if (!legacy) {
      return history(verified.ref).then(function (before) {
        return runTask(verified.ref).then(function () { return waitForRun(task, verified.ref, before, started, deadline, POLL_MS); });
      });
    }
    return legacyHistory(verified.ref, deadline).then(function (before) {
      if (before.some(function (record) { return !record.stop_time || record.exit_type === "running"; })) throw fail("root_not_ready");
      // Mutating calls are sent exactly once, never retried with another profile.
      return bounded(function () { return runTask(verified.ref); }, deadline).then(function () {
        return waitForLegacyRun(task, verified.ref, before, started, deadline, POLL_MS);
      });
    }).then(function () {
      return bounded(function () { return find(task, true); }, deadline).then(function (after) {
        if (after.state !== "ready" || String(after.ref.id) !== String(verified.ref.id)) throw fail("root_verify_failed");
      });
    });
  }
  function verify(op) {
    var ref = verified[op];
    delete verified[op];
    return ref && op !== "helper_repair" ? Promise.resolve({ state: "ready", ref: ref }) : find(byOp[op], true);
  }
  function performRun(op) {
    return begin(op).then(function (task) {
      return verify(op).then(function (found) {
        if (found.state !== "ready") throw fail("root_not_ready");
        var consent = op === "helper_repair" ? options.confirmPassword() : Promise.resolve({ confirmed: true });
        return consent.then(function (confirmation) {
          if (!confirmation || confirmation.confirmed !== true) throw fail("root_cancelled");
          return op === "helper_repair" ? begin(op).then(function (task) { return find(task, true); }) : found;
        }).then(function (verified) {
          if (verified.state !== "ready") throw fail("root_not_ready");
          return execute(task, verified);
        });
      }).then(function () { return readOutput(task); });
    });
  }

  return {
    state: function (op) {
      return begin(op).then(function (task) { return find(task, true); }).then(function (found) { return found.state; });
    },
    authorize: function (op) {
      delete verified[op];
      return begin(op).then(function (task) {
        return find(task).then(function (found) {
          if (found.state === "ready") { verified[op] = found.ref; return "ready"; }
          if (found.state === "conflict") throw fail("root_conflict");
          return options.confirmPassword().then(function (confirmation) {
            if (!confirmation || confirmation.confirmed !== true) return "cancelled";
            if (!legacy && (typeof confirmation.token !== "string" || !confirmation.token)) throw fail("root_verify_failed");
            var params = definition(task, options.user().name);
            if (!legacy) params.SynoConfirmPWToken = confirmation.token;
            // Re-read after the legacy client-side dialog before replacing a task.
            var checked = legacy ? begin(op).then(find) : Promise.resolve(found);
            return checked.then(function (current) {
              if (current.state === "conflict") throw fail("root_conflict");
              if (current.state === "ready") return;
              var replaced = current.state === "outdated" ? deleteTask(current.ref) : Promise.resolve();
              return replaced.then(function () { return createTask(params); });
            }).then(function () {
              return find(task);
            }).then(function (after) {
              if (after.state !== "ready") throw fail("root_verify_failed");
              verified[op] = after.ref;
              return "ready";
            });
          });
        });
      });
    },
    run: function (op) {
      if (!Object.prototype.hasOwnProperty.call(byOp, op)) return Promise.reject(fail("root_unknown_op"));
      if (options.user().admin !== true) return Promise.reject(fail("admin_required"));
      if (running[op]) return running[op];
      var pending = performRun(op).then(function (value) {
        delete running[op];
        return value;
      }, function (error) { delete running[op]; throw error; });
      running[op] = pending;
      return pending;
    },
    remove: function (op) {
      delete verified[op];
      return begin(op).then(function (task) {
        return find(task).then(function (found) {
          if (found.state === "missing") return "missing";
          if (found.state === "conflict") throw fail("root_conflict");
          return deleteTask(found.ref).then(function () {
            return find(task);
          }).then(function (after) {
            if (after.state !== "missing") throw fail("root_verify_failed");
            return "missing";
          });
        });
      });
    },
    output: function (op) {
      return begin(op).then(readOutput);
    }
  };
}`;

