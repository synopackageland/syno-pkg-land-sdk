import assert from 'node:assert/strict';
import {mkdtempSync, mkdirSync, rmSync, writeFileSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {dirname, join} from 'node:path';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {test} from 'node:test';
import {check, violations} from './check-doc-versions.mjs';

test('rejects package pins, SDK fields, prose and tagged links', () => {
  for (const text of [
    ...['sdk', 'ui', 'cli', 'templates', 'app'].flatMap(name => [
      `npm install @synopackageland/${name}@0.47.1`,
      `npm install @synopackageland/${name}@^0.47.1`,
      `npm install @synopackageland/${name}@~0.47.1`,
      `"@synopackageland/${name}": "^0.47.1"`,
      `\`@synopackageland/${name}\` 0.47.1`,
    ]),
    'sdk: 0.47.1', 'sdk: "0.47.1"', '{"sdk": "0.47.1"}',
    'backend: {runtime: python, sdk: 0.47.1}',
    'identity_gateway.sdk: 0.47.1',
    'https://github.com/synopackageland/syno-pkg-land-sdk/tree/v0.41.0/spec/api/v1',
    'https://github.com/synopackageland/syno-pkg-land-sdk/blob/v0.41.0/docs/guide.md',
    'require github.com/synopackageland/syno-pkg-land-sdk v0.47.1',
    'syno-pkg-land-app = { git = "repo", tag = "v0.47.1" }',
    'Current scaffolds use SDK 0.47.1, templates 0.39.2 and CLI 0.48.1.',
    'SDK/App **0.47.1**', 'UI `>=0.10.1 <0.21.0`', 'SDK/CLI 0.24 support',
    'controller SDK must be 0.26.0 or later', 'Pin `identity_gateway.sdk` to 0.30.0',
  ]) assert.ok(violations(text).length, text);
});

test('allows app, runtime, protocol and DSM versions and version discovery', () => {
  for (const text of [
    'version: 0.1.0', 'MyApp-1.2.3-0001-x86_64.spk', 'SynoReclaim 1.2.0 needs approval',
    'Node.js 22.19.0; Bun 1.2.3; Deno 2.1.0; DSM 7.2.1; Go 1.26.10',
    'CLI uses Node.js 22.19.0', 'UI uses Vue 3.5.0',
    'https://nodejs.org/dist/v22.19.0/node-v22.19.0-linux-x64.tar.xz',
    'sha256: 2601c893aa3217d3c0c7284c23aaa1699aac75b533c84c0315fd16b7889fd552',
    'sdk: <sdk-version>', 'sdk: "<sdk-version>"',
    'require github.com/synopackageland/syno-pkg-land-sdk v<sdk-version>',
    'npm view @synopackageland/sdk version', 'npm install --save-exact @synopackageland/ui',
    'https://github.com/synopackageland/syno-pkg-land-sdk/tree/main/spec/protocol/v1',
    'executor manifest format 1', 'schema: 2', '127.0.0.1:5000',
  ]) assert.deepEqual(violations(text), [], text);
});

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'doc-versions-'));
  t.after(() => rmSync(root, {recursive: true, force: true}));
  return {root, put(path, text) {
    const file = join(root, path);
    mkdirSync(dirname(file), {recursive: true});
    writeFileSync(file, text);
  }};
}

test('scans nested docs, READMEs and skill references, excluding changelogs and dependencies', t => {
  const {root, put} = fixture(t);
  for (const file of ['README.md', 'docs/guide.mdx', 'skills/SKILL.md', 'skills/reference/install.md', 'ts/README.md', 'templates/native-go/README.md']) {
    put(file, 'Introduction\nsdk: 0.47.1\n');
  }
  for (const file of ['CHANGELOG.md', 'docs/CHANGELOG.md', 'node_modules/pkg/README.md', 'vendor/pkg/README.md', '.herdr-project/docs/guide.md', 'src/version.ts']) {
    put(file, 'sdk: 0.47.1\n');
  }
  assert.equal(check(root).length, 6);
  assert.ok(check(root).every(finding => finding.line === 2));
});

test('website includes every locale and fails the command with a file and line', t => {
  const {root, put} = fixture(t);
  for (const locale of ['docs', 'zh-tw/docs', 'zh-cn/docs', 'ja/docs']) {
    put(`website/src/content/docs/${locale}/guide.md`, 'Example\nsdk: 0.47.1\n');
  }
  put('docs/internal-plan.md', 'SDK 0.47.1');
  assert.equal(check(root).length, 4);
  const script = fileURLToPath(new URL('./check-doc-versions.mjs', import.meta.url));
  const failed = spawnSync(process.execPath, [script, root], {encoding: 'utf8'});
  assert.equal(failed.status, 1);
  assert.match(failed.stderr, /zh-tw\/docs\/guide.md:2:/);
  for (const locale of ['docs', 'zh-tw/docs', 'zh-cn/docs', 'ja/docs']) {
    put(`website/src/content/docs/${locale}/guide.md`, 'sdk: <sdk-version>\n');
  }
  const passed = spawnSync(process.execPath, [script, root], {encoding: 'utf8'});
  assert.equal(passed.status, 0, passed.stderr);
  assert.match(passed.stdout, /passed \(4 files\)/);
});
