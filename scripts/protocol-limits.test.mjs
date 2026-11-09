import {test} from 'node:test';
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';

test('every transport uses the generated shared reply bounds', () => {
 const script = fileURLToPath(new URL('./protocol-limits.mjs', import.meta.url));
 const check = spawnSync(process.execPath, [script, '--check'], {encoding:'utf8'});
 assert.equal(check.status, 0, check.stderr);
});

// The existing executor source owns slot capacity; client releases reuse its binary.
test('delegate admission capacity matches the shipped executor', () => {
 const script = fileURLToPath(new URL('./admission-limits.mjs', import.meta.url));
 const check = spawnSync(process.execPath, [script, '--check'], {encoding:'utf8'});
 assert.equal(check.status, 0, check.stderr);
});
