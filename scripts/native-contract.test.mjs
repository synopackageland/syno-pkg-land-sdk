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
  for(const file of ['scripts/native-contract.mjs', 'spec/native/v1/baseline.json', 'spec/native/v1/contract.json', 'ts/src/mobile/contract.gen.ts']) writeFileSync(join(work, file), readFileSync(join(root, file)))
  const spec = JSON.parse(readFileSync(join(work, 'spec/native/v1/contract.json')))
  spec.$defs.NativeReady.properties.probe = {type: 'string'}
  writeFileSync(join(work, 'spec/native/v1/contract.json'), JSON.stringify(spec))
  assert.equal(spawnSync(process.execPath, ['scripts/native-contract.mjs', '--check'], {cwd: work}).status, 1)
  execFileSync(process.execPath, ['scripts/native-contract.mjs'], {cwd: work})
  assert.match(readFileSync(join(work, 'ts/src/mobile/contract.gen.ts'), 'utf8'), /readonly probe\?: string/)
  execFileSync(process.execPath, ['scripts/native-contract.mjs', '--check'], {cwd: work})
 } finally {rmSync(work, {recursive: true, force: true})}
})

const baseline = JSON.parse(readFileSync(join(root, 'spec/native/v1/baseline.json')))
function runCandidate(mutate) {
 const work = mkdtempSync(join(tmpdir(), 'native-compat-'))
 try {
  for(const dir of ['scripts', 'spec/native/v1', 'ts/src/mobile']) mkdirSync(join(work, dir), {recursive: true})
  for(const file of ['scripts/native-contract.mjs', 'spec/native/v1/baseline.json', 'ts/src/mobile/contract.gen.ts']) writeFileSync(join(work, file), readFileSync(join(root, file)))
  const candidate = structuredClone(baseline)
  mutate(candidate)
  writeFileSync(join(work, 'spec/native/v1/contract.json'), JSON.stringify(candidate))
  const generated = spawnSync(process.execPath, ['scripts/native-contract.mjs'], {cwd: work, encoding: 'utf8'})
  const checked = spawnSync(process.execPath, ['scripts/native-contract.mjs', '--check'], {cwd: work, encoding: 'utf8'})
  return {generated, checked}
 } finally {rmSync(work, {recursive: true, force: true})}
}
const incompatible = {
 'removed definition': s => {delete s.$defs.NativeReady},
 'removed field': s => {delete s.$defs.NativeLaunch.properties.link},
 'renamed field': s => {s.$defs.NativeLaunch.properties.route = s.$defs.NativeLaunch.properties.link; delete s.$defs.NativeLaunch.properties.link},
 'new required field': s => {s.$defs.NativeReady.properties.probe = {type: 'string'}; s.$defs.NativeReady.required.push('probe')},
 'optional becomes required': s => {s.$defs.NativeLaunch.required.push('link')},
 'narrowed numeric type': s => {s.$defs.NativeReady.properties.fcp.type = 'integer'},
 'changed type': s => {s.$defs.NativeReady.properties.fcp.type = 'string'},
 'narrowed enum': s => {s.$defs.NativeTransfer.properties.state.enum.pop()},
 'enum replaces string': s => {s.$defs.NativeShell.properties.name = {enum: ['Shell']}},
 'removed union branch': s => {s.$defs.NativeMessage.anyOf.pop()},
 'narrowed nested union': s => {s.$defs.NativeCapabilities.properties.download.anyOf[1].properties.maxBase64.maximum = 1},
 'narrowed minimum': s => {s.$defs.NativeReady.properties.fcp.minimum = 1},
 'new maximum': s => {s.$defs.NativeReady.properties.fcp.maximum = 100},
 'narrowed string bound': s => {s.$defs.NativeLaunch.properties.link.maxLength = 100},
 'new pattern': s => {s.$defs.NativeShell.properties.name.pattern = '^shell$'},
 'changed constant': s => {s.$defs.NativeReady.properties.type.const = 'loaded'},
 'changed reference': s => {s.$defs.NativeMessage.anyOf[0].$ref = '#/$defs/NativeReady'},
 'changed bridge': s => {s.bridgeName = 'OtherBridge'},
 'changed event name': s => {s.eventName = 'other-event'},
 'changed protocol': s => {s.protocol = 2},
 'unstable contract': s => {s.stability = 'experimental'},
 'unrecognized validation keyword': s => {s.$defs.NativeReady.properties.fcp.multipleOf = 2},
}
for(const [name, mutate] of Object.entries(incompatible)) test(`stable contract rejects ${name} before generation and during check`, () => {
 const {generated, checked} = runCandidate(mutate)
 assert.equal(generated.status, 1, generated.stderr)
 assert.match(generated.stderr, /Native v1 compatibility/)
 assert.equal(checked.status, 1)
 assert.match(checked.stderr, /Native v1 compatibility/)
})
const additive = {
 'unchanged baseline': () => {},
 'optional field': s => {s.$defs.NativeReady.properties.probe = {type: 'string'}},
 'optional nested capability': s => {s.$defs.NativeCapabilities.properties.download.anyOf[1].properties.resume = {type: 'boolean'}},
 'optional capability': s => {s.$defs.NativeCapabilities.properties.haptics = {type: 'boolean'}},
 'enum extension': s => {s.$defs.NativeShell.properties.platform.enum.push('other')},
 'new message and event': s => {s.$defs.NativePing = {type: 'object', properties: {v: {const: 1}, type: {const: 'ping'}}, required: ['v', 'type'], additionalProperties: false}; s.$defs.NativeMessage.anyOf.push({$ref: '#/$defs/NativePing'}); s.$defs.NativeEvent.anyOf.push({$ref: '#/$defs/NativePing'})},
 'reordered union and enum': s => {s.$defs.NativeMessage.anyOf.reverse(); s.$defs.NativeTransfer.properties.state.enum.reverse()},
 'wider bounds': s => {s.$defs.NativeLaunch.properties.link.maxLength += 10},
 'documentation': s => {s.$defs.NativeReady.description = 'First contentful paint notification'},
}
for(const [name, mutate] of Object.entries(additive)) test(`stable contract accepts ${name}`, () => {
 const {generated, checked} = runCandidate(mutate)
 assert.equal(generated.status, 0, generated.stderr)
 assert.equal(checked.status, 0, checked.stderr)
})
