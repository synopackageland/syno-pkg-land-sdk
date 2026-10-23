// The release record contains reproducible-build results. Ship its runtime
// inventory in both integrity-protected SDK distributions; never fetch it.
import { readFileSync, writeFileSync } from 'node:fs';
import { isDeepStrictEqual } from 'node:util';

const root = new URL('../', import.meta.url);
const read = name => JSON.parse(readFileSync(new URL(name, root), 'utf8'));
const versionPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
const version = read('ts/package.json').version;
if (!versionPattern.test(version) || read('node/package.json').version !== version) throw new Error('SDK package versions differ or are invalid');
const release = read(`release/v${version}.json`);
if (release.version !== version || typeof release.executorVersion !== 'string' || !versionPattern.test(release.executorVersion)) throw new Error('Invalid executor release version');
const d = release.executorDigests;
if (!d || typeof d.goToolchain !== 'string' || !/^go1\.\d+\.\d+$/.test(d.goToolchain)) throw new Error('Invalid executor Go toolchain');
const archs = ['x86_64', 'aarch64', 'armv7'];
const hashes = value => Object.fromEntries(archs.map(arch => {
  if (typeof value?.[arch] !== 'string' || !/^[a-f0-9]{64}$/.test(value[arch])) throw new Error(`Invalid executor digest: ${arch}`);
  return [arch, value[arch]];
}));
const manifest = { formatVersion: 1, sdkVersion: version, executorVersion: release.executorVersion,
  executorDigests: { ...hashes(d), lifecycle: hashes(d.lifecycle), goToolchain: d.goToolchain } };
const content = JSON.stringify(manifest, null, 2) + '\n';
if (process.argv.includes('--verify-build')) {
  const built = JSON.parse(readFileSync(0, 'utf8'));
  if (!isDeepStrictEqual(built[version], manifest.executorDigests)) throw new Error('Reproducible executor build differs from the SDK manifest');
}
for (const name of ['executor-manifest.json', 'ts/executor-manifest.json']) {
  let current;
  try { current = readFileSync(new URL(name, root), 'utf8'); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  if (current === content) continue;
  if (process.argv.includes('--check')) throw new Error(`${name} differs from the release record; run node scripts/executor-manifest.mjs`);
  writeFileSync(new URL(name, root), content);
}
