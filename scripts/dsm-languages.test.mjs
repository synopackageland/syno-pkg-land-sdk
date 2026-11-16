import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const root = fileURLToPath(new URL('..', import.meta.url))
const source = 'spec/language/dsm-languages.json'
const generated = ['api/dsm_languages.gen.go', 'compose/cmd/plan/dsm_languages.gen.go', 'rust/app/src/dsm_languages.rs', 'ts/src/dsmLanguages.gen.ts']

test('the Go, Rust and TypeScript DSM language tables come from one source', () => {
  const work = mkdtempSync(join(tmpdir(), 'dsm-languages-'))
  try {
    for (const dir of ['scripts', 'spec/language', 'api', 'compose/cmd/plan', 'rust/app/src', 'ts/src']) mkdirSync(join(work, dir), { recursive: true })
    cpSync(join(root, 'scripts/dsm-languages.mjs'), join(work, 'scripts/dsm-languages.mjs'))
    cpSync(join(root, source), join(work, source))
    const run = (...args) => spawnSync(process.execPath, [join(work, 'scripts/dsm-languages.mjs'), ...args], { encoding: 'utf8' })
    assert.equal(run('--check').status, 1, 'missing tables fail the check')
    assert.equal(run().status, 0)
    for (const name of generated) assert.equal(readFileSync(join(work, name), 'utf8'), readFileSync(join(root, name), 'utf8'), name)
    assert.equal(run('--check').status, 0)
    // A table edited by hand, or a source changed without regenerating, fails.
    writeFileSync(join(work, generated[2]), readFileSync(join(work, generated[2]), 'utf8').replace('"zh-TW"', '"zh-HK"'))
    assert.equal(run('--check').status, 1)
    run()
    const spec = JSON.parse(readFileSync(join(work, source), 'utf8'))
    spec.languages.push({ dsm: 'rus', locale: 'ru' })
    writeFileSync(join(work, source), JSON.stringify(spec))
    const stale = run('--check')
    assert.equal(stale.status, 1)
    assert.match(stale.stderr, /differs from spec\/language\/dsm-languages\.json/)
    spec.languages.push({ dsm: 'rus', locale: 'ru' })
    writeFileSync(join(work, source), JSON.stringify(spec))
    assert.notEqual(run().status, 0, 'duplicate codes are refused')
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
})

test('no handwritten copy of the DSM language table remains', () => {
  // A literal cht→zh-TW pair outside the source, the generated tables and the
  // shared test cases is a second copy that could drift. sharedfolder's
  // message switch translates one message per language; it is no lookup table.
  const listed = spawnSync('git', ['grep', '-l', '-E', `cht["']?[:,]?[[:space:]]*["']zh-TW`, '--', '.'], { cwd: root, encoding: 'utf8' })
  const copies = listed.stdout.split('\n').filter(Boolean)
    .filter(name => ![source, ...generated, 'spec/language/app-status-cases.json', 'sharedfolder/message.go'].includes(name))
    .filter(name => !/(_test\.go|\.test\.ts|tests\.rs|\.md)$/.test(name))
  assert.deepEqual(copies, [])
})

test('every generated UI locale is one the TypeScript SDK ships', () => {
  const { languages } = JSON.parse(readFileSync(join(root, source), 'utf8'))
  const i18n = readFileSync(join(root, 'ts/src/i18n.ts'), 'utf8')
  const shipped = JSON.parse(/uiLocales:readonly UiLocale\[\]=(\[[^\]]*\])/.exec(i18n)[1].replaceAll("'", '"'))
  for (const { locale } of languages) assert.ok(shipped.includes(locale), locale)
})
