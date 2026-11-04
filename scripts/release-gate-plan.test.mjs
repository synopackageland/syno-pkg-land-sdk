import assert from 'node:assert/strict';
import {test} from 'node:test';
import {spawnSync} from 'node:child_process';
import {mkdtempSync, mkdirSync, copyFileSync, writeFileSync, rmSync, readFileSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {gatePlan} from './release-gate-plan.mjs';

test('basic release plan excludes integrations, full retains them', () => {
  assert.deepEqual(gatePlan(), ['basic']);
  assert.deepEqual(gatePlan(true), ['basic', 'integrations', 'registry']);
  const basic = readFileSync(new URL('./basic-gate.sh', import.meta.url), 'utf8');
  assert.doesNotMatch(basic, /qemu|verify-build|executor-digests|GOCACHE=|node22-test|python38-test|test-npm-publish|gate-full/);
  const full = readFileSync(new URL('./gate-full.sh', import.meta.url), 'utf8');
  for (const part of ['--verify-build', 'GOCACHE=', 'node22-test', 'python38-test', 'for arch in']) assert.ok(full.includes(part), part);
});
test('real dispatcher selects basic by default and integrations only explicitly, using fake workloads', () => {
  const root = mkdtempSync(join(tmpdir(), 'sdk-gate-plan-'));
  try {
    mkdirSync(join(root, 'scripts'));
    for (const file of ['gate.sh', 'release-gate-plan.mjs']) copyFileSync(new URL(file, import.meta.url), join(root, 'scripts', file));
    for (const [file, body] of [['basic-gate.sh', 'echo basic'], ['gate-full.sh', 'echo integrations'], ['test-npm-publish.py', 'print("registry")']]) {
      writeFileSync(join(root, 'scripts', file), body + '\n');
    }
    for (const [args, value, want] of [[[], '', gatePlan()], [['--fast'], '', gatePlan()], [['--full'], '', gatePlan(true)], [[], '1', gatePlan(true)]]) {
      const r = spawnSync('bash', [join(root, 'scripts/gate.sh'), ...args], {encoding: 'utf8', env: {...process.env, SPL_FULL_GATE: value}});
      assert.equal(r.status, 0, r.stderr);
      assert.deepEqual(r.stdout.trim().split('\n'), want);
    }
  } finally { rmSync(root, {recursive: true, force: true}); }
});
