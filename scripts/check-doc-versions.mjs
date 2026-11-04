#!/usr/bin/env node
// User guides describe the current contract; dependency pins belong in manifests.
import {existsSync, readdirSync, readFileSync} from 'node:fs';
import {dirname, join, relative, resolve} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';

const version = String.raw`v?\d+\.\d+(?:\.(?:\d+|x|\*))?(?:-[\w.-]+)?`;
const rules = [
  ['npm package version', new RegExp(String.raw`@synopackageland/(?:sdk|ui|cli|templates|app)(?:@["'\x60~^<>=]*|["'\x60*\s]*[:=]?\s*["'\x60*~^<>=]*\s*)${version}`, 'i')],
  ['numeric SDK field', /\b(?:[\w.-]+\.)?sdk["'`]*\s*:\s*["'`]*v?\d+\.\d+/i],
  ['tagged source link', /syno-pkg-land-(?:sdk|ui|cli|templates)\/(?:tree|blob)\/v\d/i],
  ['Go SDK version', new RegExp(String.raw`syno-pkg-land-sdk(?:/executor)?\s+${version}`, 'i')],
  ['Rust SDK tag', /syno-pkg-land-app\b.*\btag\s*=\s*["']v?\d+\.\d+/i],
  ['release in prose', new RegExp(String.raw`\b(?:SDK(?:/(?:App|CLI|templates))?|CLI|UI|templates)(?:\s+(?:version|release))?\s*[\x60*~^<>=]*${version}`, 'i')],
  ['SDK pin in prose', new RegExp(String.raw`\bsdk[\x60"']*\s+(?:to|must be|below)\s*[\x60*~^<>=]*${version}`, 'i')],
];
const ignored = new Set(['.git', '.herdr-project', 'node_modules', 'vendor', 'dist', 'build', 'coverage', '.astro', '.pnpm']);
const textFile = /\.(?:md|mdx|markdown|rst|txt|html)$/i;

export function violations(text) {
  return text.split(/\r?\n/).flatMap((line, index) => {
    const rule = rules.find(([, pattern]) => pattern.test(line));
    return rule ? [{line: index + 1, rule: rule[0], text: line.trim()}] : [];
  });
}

export function documentationFiles(root) {
  const files = [];
  const website = join(root, 'website/src/content/docs');
  const websiteOnly = existsSync(website);
  function walk(dir, inDocs = false) {
    for (const entry of readdirSync(dir, {withFileTypes: true})) {
      if (ignored.has(entry.name) || entry.isSymbolicLink()) continue;
      const path = join(dir, entry.name);
      if (entry.isDirectory()) {
        walk(path, inDocs || ['docs', 'skills', 'skill'].includes(entry.name));
      } else if (entry.isFile() && !/^changelog(?:[.-]|$)/i.test(entry.name) &&
        ((inDocs && textFile.test(entry.name)) || /^readme(?:[.-]|$)/i.test(entry.name))) {
        files.push(path);
      }
    }
  }
  walk(websiteOnly ? website : root, websiteOnly);
  return files.sort();
}

export function check(root) {
  return documentationFiles(root).flatMap(file =>
    violations(readFileSync(file, 'utf8')).map(finding => ({file: relative(root, file), ...finding})));
}

export function main(args = process.argv.slice(2)) {
  if (args.length > 1) throw new Error('Usage: check-doc-versions.mjs [repo-root]');
  const root = resolve(args[0] ?? join(dirname(fileURLToPath(import.meta.url)), '..'));
  const findings = check(root);
  for (const {file, line, rule, text} of findings) {
    console.error(`${file}:${line}: ${rule}: ${text}`);
  }
  if (findings.length) {
    console.error('Use unversioned install commands, <sdk-version> with lookup instructions, and main source links.');
    process.exitCode = 1;
  } else {
    console.log(`Documentation version check passed (${documentationFiles(root).length} files).`);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) main();
