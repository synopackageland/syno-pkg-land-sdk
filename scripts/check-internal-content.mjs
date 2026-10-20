// Shared de-identification scanner, derived from the templates repository's
// scripts/test/internal-content.test.mjs at 3f375fe943eb864bf7245fadc25732e0c8749301.
// Keep this file identical in CLI, SDK and UI; no runtime dependency is needed.
import {execFileSync} from 'node:child_process';
import {readFileSync, readdirSync, lstatSync, mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {resolve, relative, join, posix} from 'node:path';
import {pathToFileURL} from 'node:url';

export const INTERNAL = [
  ['internal domain', /\b[a-z0-9-]+\.syno(?![\w.-])|\bsynology\.inc\b/],
  ['internal mailbox', /[a-z0-9._%+-]+@[a-z0-9.-]*synology\.(?:com|inc|cn)\b/i],
  ['private address', /\b(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})\b/],
  ['shared test credential', /\b(?:admin|root)\s*[:=]\s*(?:password|passwd|changeme|1234(?:56)?)\b/i],
  ['machine path', /\/Users\/[A-Za-z]/],
  ['thread artifact path', /\bthreads?\/t-\d+/],
  ['internal task id', /\bt-\d{3,4}\b/],
  ['internal review note', new RegExp('\\b(?:' + [
    ['finish', 'review'], ['material', 'findings'], ['disposition', 'is'], ['NAS', 'browser', 'readback'],
  ].map(words => words.join('\\s+')).join('|') + ')\\b', 'i')],
];

/** Optional local policy: one literal term per line, or regex:<source>.
 * Blank lines and # comments are ignored; term:<literal> is also accepted.
 * Store this file outside the repository. Never print its terms or patterns.
 */
export function loadRules(file = process.env.SPL_INTERNAL_DENYLIST, warn = console.warn) {
  if (!file) {
    warn('internal-content: SPL_INTERNAL_DENYLIST is unset; running generic checks only');
    return [...INTERNAL];
  }
  let content;
  try { content = readFileSync(file, 'utf8'); }
  catch { throw new Error('internal-content: cannot read the configured denylist'); }
  const local = content.split(/\r?\n/).flatMap((raw, i) => {
    const line = raw.trim();
    if (!line || line.startsWith('#')) return [];
    const regex = line.startsWith('regex:');
    const value = line.replace(/^(?:regex|term):/, '');
    if (!value) throw new Error(`internal-content: empty denylist rule on line ${i + 1}`);
    try {
      const source = regex ? value : value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
      return [[`local denylist rule ${i + 1}`, new RegExp(source, 'i')]];
    } catch { throw new Error(`internal-content: invalid denylist pattern on line ${i + 1}`); }
  });
  return [...INTERNAL, ...local];
}
let cachedRules;
const defaultRules = () => cachedRules ??= loadRules();

export function findings(file, content, rules = defaultRules()) {
  if (content.includes('\0')) return []; // Binary assets; never print their bytes.
  return content.split('\n').flatMap((line, i) => rules
    .filter(([, pattern]) => pattern.test(line))
    .map(([label]) => `${file}:${i + 1}: ${label}`));
}

export function repositoryFindings(root, rules = defaultRules()) {
  const files = execFileSync('git', ['ls-files', '-z', '--cached', '--others', '--exclude-standard'], {cwd: root, encoding: 'utf8'}).split('\0').filter(Boolean);
  // Every tracked/untracked source file is scanned, including this scanner and its tests.
  return [...new Set(files)].flatMap(file =>
    findings(file, file, rules).concat(lstatSync(join(root, file)).isFile() ? findings(file, readFileSync(join(root, file), 'utf8'), rules) : []));
}

export function directoryFindings(root, rules = defaultRules()) {
  return readdirSync(root, {recursive: true, withFileTypes: true}).flatMap(entry => {
    const file = join(entry.parentPath, entry.name);
    const name = relative(root, file);
    return findings(name, name, rules).concat(entry.isFile() ? findings(name, readFileSync(file, 'utf8'), rules) : []);
  });
}

export function tarballFindings(tarball, rules = defaultRules()) {
  const entries = execFileSync('tar', ['-tzf', tarball], {encoding: 'utf8'}).split('\n').filter(Boolean);
  return entries.flatMap(file => {
    if (posix.isAbsolute(file) || file.split('/').includes('..')) throw new Error('Unsafe tarball entry');
    const result = findings(file, file, rules);
    if (/(?:^|\/)check-internal-content(?:\.test)?\.mjs$/.test(file)) result.push(`${file}: scanner tooling must not be published`);
    if (!file.endsWith('/')) result.push(...findings(file, execFileSync('tar', ['-xOzf', tarball, file], {maxBuffer: 64 * 1024 * 1024}).toString('utf8'), rules));
    return result;
  });
}

export function packageFindings(root, rules = defaultRules()) {
  const work = mkdtempSync(join(tmpdir(), 'content-pack-'));
  try {
    // The gate builds first. Scan the actual artifact without rerunning hooks.
    const [packed] = JSON.parse(execFileSync('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', work], {cwd: root, encoding: 'utf8'}));
    return tarballFindings(join(work, packed.filename), rules);
  } finally { rmSync(work, {recursive: true, force: true}); }
}

if (import.meta.url === pathToFileURL(resolve(process.argv[1] ?? '')).href) {
  const [mode, input] = process.argv.slice(2);
  if (!['--repo', '--directory', '--tarball', '--package'].includes(mode) || !input || process.argv.length !== 4) {
    console.error('usage: node scripts/check-internal-content.mjs --repo|--directory|--tarball|--package <path>');
    process.exit(2);
  }
  const check = {'--repo': repositoryFindings, '--directory': directoryFindings, '--tarball': tarballFindings, '--package': packageFindings}[mode];
  const result = check(resolve(input));
  if (result.length) { console.error(result.join('\n')); process.exit(1); }
  console.log('internal-content: clean');
}
