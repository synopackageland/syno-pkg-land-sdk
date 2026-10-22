import assert from 'node:assert/strict';
import {execFileSync, spawnSync} from 'node:child_process';
import {mkdtempSync, mkdirSync, writeFileSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {test} from 'node:test';
import {INTERNAL, loadRules, findings, repositoryFindings, directoryFindings, tarballFindings} from './check-internal-content.mjs';

const scanner = fileURLToPath(new URL('./check-internal-content.mjs', import.meta.url));
const fakeAddress = ['10', '0', '0', '5'].join('.');
const fakeTask = ['t', 1234].join('-');

test('generic policy detects synthetic forbidden inputs without echoing content', () => {
  const values = [fakeAddress, ['192', '168', '1', '2'].join('.'), ['172', '31', '2', '3'].join('.'),
    ['', 'Users', 'example', 'project'].join('/'), 'threads/' + fakeTask, fakeTask,
    ['qa', ['synology', 'com'].join('.')].join('@'), ['npm', 'synology', 'inc'].join('.'),
    ['admin', 'password'].join(':'), ['NAS', 'browser', 'readback'].join(' ')];
  for (const value of values) {
    const result = findings('fixture', value, INTERNAL);
    assert.ok(result.length, value);
    assert.ok(result.every(line => !line.endsWith(': ' + value)), value);
  }
});

test('public examples and DSM identifiers remain allowed by the generic policy', () => {
  for (const value of ['nas.example', '192.0.2.1', '198.51.100.1', '203.0.113.1', '127.0.0.1', '/home/developer/project', 'lab-box-1', 'qa-user1', 'dsm.syno-app-portal.Demo.conf', '.syno-pkg-land-ui-root']) assert.deepEqual(findings('fixture', value, INTERNAL), [], value);
});

test('optional local policy supports literal terms and regexes and fails closed on bad input', () => {
  const root = mkdtempSync(join(tmpdir(), 'content-policy-'));
  try {
    const policy = join(root, 'denylist');
    writeFileSync(policy, '# local examples\nlab-box-1\nterm:qa.user1\nregex:\\bqa-user[0-9]+\\b\n');
    const rules = loadRules(policy);
    for (const value of ['LAB-BOX-1', 'qa.user1', 'qa-user2']) assert.match(findings('fixture', value, rules).join('\n'), /local denylist rule/);
    assert.deepEqual(findings('fixture', 'qaXuser1', rules), []);
    assert.throws(() => loadRules(join(root, 'missing')), /cannot read the configured denylist/);
    writeFileSync(policy, 'regex:[\n');
    assert.throws(() => loadRules(policy), /invalid denylist pattern on line 1/);
    const warnings = [];
    assert.deepEqual(loadRules('', message => warnings.push(message)), INTERNAL);
    assert.match(warnings.join('\n'), /unset; running generic checks only/);
  } finally { rmSync(root, {recursive: true, force: true}); }
});

test('repository, generated output and packed bytes honor injected local policy without source exemptions', () => {
  const root = mkdtempSync(join(tmpdir(), 'content-scan-'));
  try {
    const policy = join(root, 'denylist');
    writeFileSync(policy, 'lab-box-1\nqa-user1\n');
    const rules = loadRules(policy);
    const source = join(root, 'source');
    mkdirSync(source);
    execFileSync('git', ['init', '-q', source]);
    mkdirSync(join(source, 'package'));
    writeFileSync(join(source, 'package', 'README.md'), 'public example');
    execFileSync('git', ['add', '.'], {cwd: source});
    assert.deepEqual(repositoryFindings(source, rules), []);
    writeFileSync(join(source, 'untracked.md'), 'lab-box-1');
    assert.match(repositoryFindings(source, rules).join('\n'), /untracked.md:1: local denylist rule/);
    mkdirSync(join(source, 'scripts'));
    for (const name of ['check-internal-content.mjs', 'check-internal-content.test.mjs']) writeFileSync(join(source, 'scripts', name), 'qa-user1');
    assert.equal(repositoryFindings(source, rules).filter(line => line.includes('scripts/')).length, 2);
    writeFileSync(join(source, 'package', 'README.md'), fakeTask);
    assert.match(directoryFindings(join(source, 'package'), rules).join('\n'), /internal task id/);
    const tarball = join(root, 'package.tgz');
    execFileSync('tar', ['-czf', tarball, '-C', source, 'package']);
    assert.match(tarballFindings(tarball, rules).join('\n'), /internal task id/);
    writeFileSync(join(source, 'package', 'README.md'), 'qa-user1');
    execFileSync('tar', ['-czf', tarball, '-C', source, 'package']);
    const cli = spawnSync(process.execPath, [scanner, '--tarball', tarball], {encoding: 'utf8', env: {...process.env, SPL_INTERNAL_DENYLIST: policy}});
    assert.equal(cli.status, 1);
    assert.match(cli.stderr, /local denylist rule/);
    assert.doesNotMatch(cli.stderr, /qa-user1/);
    writeFileSync(join(source, 'package', 'README.md'), 'public example');
    mkdirSync(join(source, 'package', 'scripts'));
    for (const name of ['check-internal-content.mjs', 'check-internal-content.test.mjs']) writeFileSync(join(source, 'package', 'scripts', name), 'public example');
    execFileSync('tar', ['-czf', tarball, '-C', source, 'package']);
    assert.equal(tarballFindings(tarball, rules).filter(line => line.includes('tooling must not be published')).length, 2);
    const env = {...process.env};
    delete env.SPL_INTERNAL_DENYLIST;
    const generic = spawnSync(process.execPath, [scanner, '--directory', join(source, 'package')], {encoding: 'utf8', env});
    assert.equal(generic.status, 0);
    assert.match(generic.stderr, /unset; running generic checks only/);
    writeFileSync(join(source, 'package', 'README.md'), fakeAddress);
    const blocked = spawnSync(process.execPath, [scanner, '--directory', join(source, 'package')], {encoding: 'utf8', env});
    assert.equal(blocked.status, 1);
    assert.match(blocked.stderr, /private address/);
  } finally { rmSync(root, {recursive: true, force: true}); }
});
