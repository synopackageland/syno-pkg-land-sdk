import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const root = fileURLToPath(new URL('../', import.meta.url));
test('release records generate identical Go/npm manifests and reject stale, incomplete or mixed metadata', () => {
  const work = mkdtempSync(join(tmpdir(), 'executor-manifest-'));
  try {
    for (const dir of ['scripts', 'ts', 'node', 'release']) mkdirSync(join(work, dir));
    cpSync(join(root, 'scripts/executor-manifest.mjs'), join(work, 'scripts/executor-manifest.mjs'));
    const release = JSON.parse(readFileSync(join(root, 'release/v0.27.0.json')));
    const save = () => writeFileSync(join(work, 'release/v0.27.0.json'), JSON.stringify(release));
    save();
    for (const dir of ['ts', 'node']) writeFileSync(join(work, dir, 'package.json'), JSON.stringify({ version: '0.27.0' }));
    const run = (...args) => spawnSync(process.execPath, [join(work, 'scripts/executor-manifest.mjs'), ...args], { encoding: 'utf8', input: JSON.stringify({ '0.27.0': release.executorDigests }) });
    assert.equal(run().status, 0);
    const bytes = readFileSync(join(work, 'executor-manifest.json'), 'utf8');
    assert.equal(readFileSync(join(work, 'ts/executor-manifest.json'), 'utf8'), bytes);
    assert.deepEqual(JSON.parse(bytes), { formatVersion: 1, sdkVersion: release.version, executorVersion: release.executorVersion, executorDigests: release.executorDigests });
    assert.equal(run('--check').status, 0);
    assert.equal(run('--check', '--verify-build').status, 0);
    const wrong = spawnSync(process.execPath, [join(work, 'scripts/executor-manifest.mjs'), '--verify-build'], { input: '{}', encoding: 'utf8' });
    assert.notEqual(wrong.status, 0);
    writeFileSync(join(work, 'ts/executor-manifest.json'), '{}');
    assert.notEqual(run('--check').status, 0);
    assert.equal(run().status, 0);
    for (const change of [
      () => { release.executorDigests.armv7 = 'bad'; },
      () => { delete release.executorDigests.lifecycle.aarch64; },
      () => { release.executorVersion = 'main'; },
      () => { release.version = '0.28.0'; },
    ]) {
      change(); save(); assert.notEqual(run().status, 0);
      Object.assign(release, JSON.parse(readFileSync(join(root, 'release/v0.27.0.json'))));
    }
  } finally { rmSync(work, { recursive: true, force: true }); }
});

test('committed Go/npm manifests match the current release and npm exposes the manifest', () => {
  const result = spawnSync(process.execPath, ['scripts/executor-manifest.mjs', '--check'], { cwd: root, encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  const pkg = JSON.parse(readFileSync(join(root, 'ts/package.json')));
  assert.ok(pkg.files.includes('executor-manifest.json'));
  assert.equal(pkg.exports['./executor-manifest.json'], './executor-manifest.json');
});
