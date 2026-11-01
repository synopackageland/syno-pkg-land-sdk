// Compare committed module payloads with a fresh build without repairing them.
import { spawnSync } from 'node:child_process'
import { mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

export function checkNodeDist(root = fileURLToPath(new URL('../', import.meta.url))) {
  const work = mkdtempSync(join(tmpdir(), 'sdk-dist-'))
  try {
    const node = join(root, 'node')
    const built = spawnSync(process.execPath, [join(node, 'node_modules/typescript/bin/tsc'),
      '-p', join(node, 'tsconfig.build.json'), '--outDir', work], { encoding: 'utf8' })
    if (built.status !== 0) throw new Error(`Node build failed: ${built.stdout}${built.stderr}`)
    const dist = join(node, 'dist')
    const files = directory => readdirSync(directory, { recursive: true }).sort()
    const expected = files(work)
    const actual = files(dist)
    return [...new Set([...expected, ...actual])].sort().filter(name =>
      !expected.includes(name) || !actual.includes(name) ||
      !readFileSync(join(work, name)).equals(readFileSync(join(dist, name))))
  } finally { rmSync(work, { recursive: true, force: true }) }
}

if (import.meta.url === pathToFileURL(resolve(process.argv[1] ?? '')).href) {
  const changed = checkNodeDist()
  if (changed.length) {
    console.error(`node/dist differs from a fresh build: ${changed.join(', ')}; run npm --prefix node run build`)
    process.exitCode = 1
  }
}
