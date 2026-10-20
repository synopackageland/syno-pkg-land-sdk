import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, utimesSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const root = fileURLToPath(new URL('..', import.meta.url))
test('build metadata generates both adapters and refuses stale or mixed versions', () => {
  const work = mkdtempSync(join(tmpdir(), 'sdk-version-'))
  try {
    for (const dir of ['scripts', 'node/src', 'ts', 'python/synopkgland_app', 'rust/app']) mkdirSync(join(work, dir), { recursive: true })
    cpSync(join(root, 'scripts/sdk-version.mjs'), join(work, 'scripts/sdk-version.mjs'))
    const setVersion = version => {
      for (const name of ['node', 'ts']) writeFileSync(join(work, name, 'package.json'), JSON.stringify({ version }))
      writeFileSync(join(work, 'rust/app/Cargo.toml'), `[package]\nname = "syno-pkg-land-app"\nversion = "${version}"\n`)
    }
    const run = (...args) => spawnSync(process.execPath, [join(work, 'scripts/sdk-version.mjs'), ...args], { encoding: 'utf8' })
    setVersion('7.8.9')
    assert.equal(run().status, 0)
    assert.match(readFileSync(join(work, 'node/src/sdk-version.ts'), 'utf8'), /7\.8\.9/)
    assert.match(readFileSync(join(work, 'python/synopkgland_app/_version.py'), 'utf8'), /7\.8\.9/)
    assert.equal(run('--check').status, 0)
    const generated = join(work, 'node/src/sdk-version.ts')
    const old = new Date('2000-01-01T00:00:00Z')
    utimesSync(generated, old, old)
    assert.equal(run().status, 0)
    assert.equal(statSync(generated).mtimeMs, old.getTime(), 'unchanged generation must not dirty the Git snapshot')
    setVersion('7.8.10')
    assert.notEqual(run('--check').status, 0, 'a metadata-only bump must invalidate generated constants')
    assert.equal(run().status, 0)
    assert.match(readFileSync(join(work, 'node/src/sdk-version.ts'), 'utf8'), /7\.8\.10/)
    assert.match(readFileSync(join(work, 'python/synopkgland_app/_version.py'), 'utf8'), /7\.8\.10/)
    writeFileSync(join(work, 'node/package.json'), JSON.stringify({ version: '9.0.0' }))
    assert.notEqual(run().status, 0, 'mixed language metadata must be refused')
    assert.notEqual(run('--check').status, 0)
    assert.ok(existsSync(join(work, 'node/src/sdk-version.ts')))
  } finally { rmSync(work, { recursive: true, force: true }) }
})
