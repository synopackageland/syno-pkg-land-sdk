import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { checkNodeDist } from './node-dist.mjs'

const root = fileURLToPath(new URL('../', import.meta.url))

test('committed Node module payload matches a fresh source build', () => {
  assert.deepEqual(checkNodeDist(root), [])
})

test('drift checking rejects changed, missing and extra payloads without repairing them', () => {
  const work = mkdtempSync(join(tmpdir(), 'sdk-dist-test-'))
  try {
    for (const name of ['src', 'dist', 'package.json', 'tsconfig.json', 'tsconfig.build.json']) {
      cpSync(join(root, 'node', name), join(work, 'node', name), { recursive: true })
    }
    symlinkSync(join(root, 'node/node_modules'), join(work, 'node/node_modules'), 'dir')
    const changed = ['sdk-version.js', 'sdk-version.d.ts', 'app.d.ts', 'app.js']
    for (const name of changed) writeFileSync(join(work, 'node/dist', name), '// stale\n')
    rmSync(join(work, 'node/dist/index.d.ts'))
    writeFileSync(join(work, 'node/dist/extra.js'), '// obsolete\n')
    assert.deepEqual(checkNodeDist(work), [...changed, 'index.d.ts', 'extra.js'].sort())
    assert.equal(readFileSync(join(work, 'node/dist/sdk-version.js'), 'utf8'), '// stale\n')
    // An unbuilt source edit must also be caught, even when dist was untouched.
    cpSync(join(root, 'node/dist'), join(work, 'node/dist'), { recursive: true })
    rmSync(join(work, 'node/dist/extra.js'))
    writeFileSync(join(work, 'node/src/sdk-version.ts'), 'export const SDK_VERSION = "7.8.9"\n')
    assert.deepEqual(checkNodeDist(work), ['app.d.ts', 'sdk-version.d.ts', 'sdk-version.js'])
  } finally { rmSync(work, { recursive: true, force: true }) }
})

test('release version generation rebuilds the committed Node payload', () => {
  const work = mkdtempSync(join(tmpdir(), 'sdk-release-'))
  try {
    for (const dir of ['scripts', 'ts', 'python/synopkgland_app', 'rust/app']) mkdirSync(join(work, dir), { recursive: true })
    cpSync(join(root, 'scripts/sdk-version.mjs'), join(work, 'scripts/sdk-version.mjs'))
    for (const name of ['src', 'package.json', 'tsconfig.json', 'tsconfig.build.json']) {
      cpSync(join(root, 'node', name), join(work, 'node', name), { recursive: true })
    }
    symlinkSync(join(root, 'node/node_modules'), join(work, 'node/node_modules'), 'dir')
    const pkg = JSON.parse(readFileSync(join(work, 'node/package.json')))
    pkg.version = '7.8.9'
    writeFileSync(join(work, 'node/package.json'), JSON.stringify(pkg))
    writeFileSync(join(work, 'ts/package.json'), JSON.stringify({ version: pkg.version }))
    writeFileSync(join(work, 'rust/app/Cargo.toml'), `[package]\nversion = "${pkg.version}"\n`)
    const generated = spawnSync(process.execPath, [join(work, 'scripts/sdk-version.mjs')], { encoding: 'utf8' })
    assert.equal(generated.status, 0, generated.stdout + generated.stderr)
    assert.deepEqual(checkNodeDist(work), [])
    assert.match(readFileSync(join(work, 'node/dist/app.d.ts'), 'utf8'), /node\/7\.8\.9/)
  } finally { rmSync(work, { recursive: true, force: true }) }
})
