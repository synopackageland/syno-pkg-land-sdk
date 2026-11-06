import {test} from 'node:test'
import assert from 'node:assert/strict'
import {execFileSync, spawnSync} from 'node:child_process'
import {mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {resolve, dirname, join} from 'node:path'
import {fileURLToPath} from 'node:url'
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
test('checked-in native client is generated from the current contract', () => {
 execFileSync(process.execPath, ['scripts/native-contract.mjs', '--check'], {cwd: root})
})
test('drift check fails for stale outputs and regenerates changed wire fields', () => {
 const work = mkdtempSync(join(tmpdir(), 'native-contract-'))
 try {
  for(const dir of ['scripts', 'spec/native/v1', 'ts/src/mobile']) mkdirSync(join(work, dir), {recursive: true})
  for(const file of ['scripts/native-contract.mjs', 'spec/native/v1/contract.json', 'ts/src/mobile/contract.gen.ts']) writeFileSync(join(work, file), readFileSync(join(root, file)))
  const spec = JSON.parse(readFileSync(join(work, 'spec/native/v1/contract.json')))
  spec.$defs.NativeReady.properties.probe = {type: 'string'}
  spec.$defs.NativeReady.required.push('probe')
  writeFileSync(join(work, 'spec/native/v1/contract.json'), JSON.stringify(spec))
  assert.equal(spawnSync(process.execPath, ['scripts/native-contract.mjs', '--check'], {cwd: work}).status, 1)
  execFileSync(process.execPath, ['scripts/native-contract.mjs'], {cwd: work})
  assert.match(readFileSync(join(work, 'ts/src/mobile/contract.gen.ts'), 'utf8'), /readonly probe: string/)
  execFileSync(process.execPath, ['scripts/native-contract.mjs', '--check'], {cwd: work})
 } finally {rmSync(work, {recursive: true, force: true})}
})
