// Generate runtime version identifiers from release metadata, never from a
// second handwritten version. Release updates regenerate the module's Node
// payload too; npm build uses --source-only to avoid recursive builds.
import { readFileSync, writeFileSync, existsSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const root = new URL('../', import.meta.url)
const read = name => readFileSync(new URL(name, root), 'utf8')
const version = JSON.parse(read('node/package.json')).version
if (!/^\d+\.\d+\.\d+(?:-[\w.-]+)?(?:\+[\w.-]+)?$/.test(version)) throw new Error('Invalid Node SDK package version')
const frontend = JSON.parse(read('ts/package.json')).version
const rust = /^version\s*=\s*"([^"]+)"\s*$/m.exec(read('rust/app/Cargo.toml'))?.[1]
if (frontend !== version || rust !== version) throw new Error('SDK release metadata versions differ across languages')
const files = {
  'node/src/sdk-version.ts': `// Generated from package.json by scripts/sdk-version.mjs. DO NOT EDIT.\nexport const SDK_VERSION = ${JSON.stringify(version)}\n`,
  'python/synopkgland_app/_version.py': `# Generated from release metadata by scripts/sdk-version.mjs. DO NOT EDIT.\nSDK_VERSION = ${JSON.stringify(version)}\n`,
}
const check = process.argv.includes('--check')
for (const [name, content] of Object.entries(files)) {
  const path = new URL(name, root)
  const unchanged = existsSync(path) && readFileSync(path, 'utf8') === content
  if (!check && !unchanged) writeFileSync(path, content)
  else if (check && !unchanged) {
    console.error(`${name} differs from package metadata; run node scripts/sdk-version.mjs`)
    process.exitCode = 1
  }
}
if (!check && !process.argv.includes('--source-only')) {
  const built = spawnSync('npm', ['run', 'build'], {
    cwd: fileURLToPath(new URL('node/', root)), stdio: 'inherit',
  })
  if (built.error) throw built.error
  if (built.status !== 0) process.exitCode = built.status ?? 1
}
