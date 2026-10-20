# Scheduler readback v1

How an approval task is read back from DSM and judged. Three readers follow
it: the executor (`../../../executor/tasks.go`, as root through synowebapi),
the CLI's DSM window approval (`createRootAccess` in the CLI's
`src/standalone/root-window.ts`, as the signed-in administrator) and the
native root runtime (`ROOT_RUNTIME` of `@synopackageland/sdk/native`,
`../../../ts/src/native/root-runtime.ts`), whose product tasks are Task
Scheduler tasks judged as the control task is, with their own name, header
and script; it has no boot task. All run `readback.json` in their tests (the
native runtime its control cases).

## The tasks

| Kind | DSM API | Name | Key |
|---|---|---|---|
| boot | `SYNO.Core.EventScheduler` | `<package> executor boot` | `task_name` |
| control | `SYNO.Core.TaskScheduler` | `<package> executor control` | numeric `id` |

## DSM behavior the readers rely on

Measured on DSM 7.4-90069 (x86_64, 2026-10-14):

- EventScheduler stores tasks in an SQLite table whose `task_name` is the
  PRIMARY KEY. A second `create` with the same name fails with
  `117 DUPLICATE_PRIMARY_KEY`. A boot task therefore cannot have a duplicate,
  and `get` by name is the whole lookup: no `list`.
- EventScheduler `get` of a missing name answers `success: true` without
  `data`. That, or an empty object, means the task is missing.
- Task Scheduler names repeat. Only a `list` finds the id of a name and shows
  whether the name repeats. `get` takes only the id. A missing id answers
  `4801`.
- Task Scheduler `list` ignores `name`, `owner` and `type` parameters; only
  `start`/`offset` and `limit` page it.
- The scheduler APIs are not processed one at a time: two concurrent reads
  usually take less than the same reads one after the other. Bundling them
  in one `SYNO.Entry.Request` (sequential or parallel mode) gave no steady gain.

## Rules

A reader SHOULD list Task Scheduler once per approval step, and MAY then get
the control task by the id that list showed. It MUST list again when that get
answers `4801` or a task with another id or name. A step that deletes a task
by what it read MUST use a fresh list.

The verdict, with its code:

| Code | When |
|---|---|
| `ready` | The task is exactly the canonical one (below) |
| `task_missing` | No such name |
| `task_duplicate` | The control name is listed more than once |
| `task_unverified` | Owner, kind or the answered name/id differ from the canonical definition |
| `task_outdated` | The script starts with the task's header but is not the current script |
| `task_conflict` | Any other script |

Canonical boot: owner `{"0": "root"}`, `enable: true`, `event: "bootup"`,
`operation_type: "script"`, `operation` the current script. Canonical
control: `owner` and `real_owner` `"root"`, `enable: false`,
`type: "script"`, `extra.script` the current script, and the `id` it was
listed with. The executor needs only `ready` or not; the window shows the code.

## Approval across executor upgrades

An executor upgrade keeps the existing approval when the policy is unchanged
and the new executor matches `synopkgland_executor_sha256` in the root-owned
package INFO. A policy or capability change requires an administrator's
approval again. A digest change alone does not require approval; an executor
that fails the INFO anchor check cannot use this upgrade path. See
[Package lifecycle](../../../executor/README.md#package-lifecycle) for the
anchored installation rules.

## The executor's readback in status

The executor reads its tasks back at start and then every 10
minutes (every minute after a failure), and `status` reports the verdict as
`tasks` (`../../protocol/v1`, Status). An administrator's DSM window MAY show
the App without reading the scheduler itself when `status` answers the pinned
digests, `ready: true` and `tasks.state` `ready`: the executor checked the
same tasks under the same rules, with the scripts the pinned policy digest
generates. Otherwise (an older executor without `tasks`, `checking`,
`stale`, `failed`, other digests) it reads the tasks back as above. Approval,
the control task's start, stop and purge, and a scope selection MUST still
read both tasks back in the same step, whatever `tasks` says.

Neither check guards against the package itself: the window's code and the
CGI that relays `status` belong to the package account. Both guard against a
change by someone else who can edit root tasks (another administrator, root).
The difference is when: an edit after the executor's last readback is seen up
to one period (10 minutes, 15 at most) later instead of when the window
opens. Until then the window shows the App; a changed control task is still
refused at the next start, stop or purge, and a removed boot task only means
that the executor does not start at the next boot.

## readback.json

`{package, policyDigest, outdatedPolicyDigest, cases}`. Each case is
`{name, kind, list?, get, expect}`: `list` is the Task Scheduler list rows
(control only), `get` the `data` of the `get` answer (`null` for none). In
strings, `@NAME@` is the task name, `@SCRIPT@` its current script and
`@OUTDATED@` its script under `outdatedPolicyDigest`.
