import { test } from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

// src/frames.ts and src/refusals.ts are generated from spec/ and committed.
test('the generated files match spec/', () => {
  const script = fileURLToPath(new URL('../../scripts/generate.mjs', import.meta.url))
  const run = spawnSync(process.execPath, [script, '--check'], { encoding: 'utf8' })
  assert.equal(run.status, 0, run.stderr)
})
