// Generate the Go, Rust and TypeScript DSM display-language tables from
// spec/language/dsm-languages.json, so the three SDKs never hold a second
// handwritten copy. --check fails when a generated file differs.
import { readFileSync, writeFileSync, existsSync } from 'node:fs'

const root = new URL('../', import.meta.url)
const source = 'spec/language/dsm-languages.json'
const { languages } = JSON.parse(readFileSync(new URL(source, root), 'utf8'))
const codes = new Set(), locales = new Set()
for (const { dsm, locale } of languages) {
  if (!/^[a-z]{3}$/.test(dsm) || dsm === 'def') throw new Error(`invalid DSM language code ${dsm}`)
  if (!/^[a-z]{2}(-[A-Z]{2})?$/.test(locale)) throw new Error(`invalid UI locale ${locale}`)
  if (codes.has(dsm) || locales.has(locale)) throw new Error(`duplicate entry ${dsm} ${locale}`)
  codes.add(dsm); locales.add(locale)
}
const header = comment => `${comment} Generated from ${source} by scripts/dsm-languages.mjs. DO NOT EDIT.\n`
const goTable = pkg => header('//') + `
package ${pkg}

// dsmLocales maps DSM display-language codes to the SDK's UI locales.
var dsmLocales = map[string]string{
${languages.map(({ dsm, locale }) => `\t${JSON.stringify(dsm)}: ${JSON.stringify(locale)},`).join('\n')}
}
`
export const files = {
  'api/dsm_languages.gen.go': goTable('api'),
  'compose/cmd/plan/dsm_languages.gen.go': goTable('main'),
  'rust/app/src/dsm_languages.rs': header('//') + `
/// DSM display-language codes and the SDK UI locale each one maps to.
pub(crate) const DSM_LOCALES: &[(&str, &str)] = &[
${languages.map(({ dsm, locale }) => `    (${JSON.stringify(dsm)}, ${JSON.stringify(locale)}),`).join('\n')}
];
`,
  'ts/src/dsmLanguages.gen.ts': header('//') +
    `/** DSM display-language codes and the SDK UI locale each one maps to. */\n` +
    `export const dsmLocales={${languages.map(({ dsm, locale }) => `${dsm}:'${locale}'`).join(',')}} as const\n`,
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const check = process.argv.includes('--check')
  for (const [name, content] of Object.entries(files)) {
    const path = new URL(name, root)
    const unchanged = existsSync(path) && readFileSync(path, 'utf8') === content
    if (!check && !unchanged) writeFileSync(path, content)
    else if (check && !unchanged) {
      console.error(`${name} differs from ${source}; run node scripts/dsm-languages.mjs`)
      process.exitCode = 1
    }
  }
}
