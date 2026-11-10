// Every literal clock must be either the shared operation default or an
// explicitly reviewed non-failure timer. Exact statements make changes visible.
import {execFileSync} from 'node:child_process';
import {readFileSync} from 'node:fs';
import {resolve, dirname} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
export const normalize = value => value.trim().replace(/\s+/g, ' ');
const number = String.raw`\b\d[\d_]*(?:\.\d+)?\b`;
const numeric = new RegExp(number);

// Split a call's arguments, retaining nested callbacks, strings and multiline
// expressions. This is intentionally conservative; unrecognized literal clocks
// require review rather than becoming an unchecked default.
function argsAt(source, start) {
 const args=[]; let depth=0, quote='', escaped=false, from=start;
 for(let i=start;i<source.length;i++) {
  const c=source[i];
  if(quote) {if(escaped) escaped=false; else if(c==='\\') escaped=true; else if(c===quote) quote=''; continue;}
  if('"\'`'.includes(c)) {quote=c; continue;}
  if('([{'.includes(c)) depth++;
  else if(')]}'.includes(c)) {if(depth===0) {args.push(source.slice(from,i));return args;} depth--;}
  else if(c===','&&depth===0) {args.push(source.slice(from,i));from=i+1;}
 }
 return args;
}

export function literalClocks(file, source) {
 if(/(?:^|\/)(?:test|tests|testdata|node_modules|dist|target|\.git)(?:\/|$)|(?:_test\.go|[.-]test\.[^/]+|test[-_][^/]+\.py)$/.test(file)) return [];
 if(!/\.(?:go|[cm]?js|tsx?|vue|py|sh|ya?ml)$/.test(file)) return [];
 const clocks=new Map();
 const add=(offset,text)=>{
  const line=source.slice(0,offset).split('\n').length;
  const statement=normalize(text);
  clocks.set(`${line}:${statement}`,{file,line,statement});
 };
 let offset=0;
 for(const line of source.split('\n')) {
  const code=line.trim();
  if(!/^(?:\/\/|#|\*|\/\*)/.test(code)) {
   // Literal Go durations, including the implicit one in time.Second. Unit
   // conversion of a caller-supplied value does not invent another budget.
   const units=[...line.matchAll(/\btime\.(?:Nanosecond|Microsecond|Millisecond|Second|Minute|Hour)\b/g)];
   const literalUnit=units.some(m=>{
    const before=line.slice(0,m.index).trimEnd();
    return !before.endsWith('*') || /\d\s*\*$/.test(before);
   });
   const settings=/(?:timeout|deadline|budget|interval|grace|_MS|_SECONDS)\w*\s*[:=]\s*[\d_]+/i.test(line);
   if(literalUnit||settings||/\btimeout\s*=\s*\d/.test(line)||/\.timeout\s+\d/.test(line)||
    /time\.Duration\(\s*\d|(?:monotonic|Date\.now|performance\.now)\(\)\s*\+\s*\d/.test(line)) add(offset,line);
  }
  offset+=line.length+1;
 }
 const declarations=new Map([...source.matchAll(/\b(?:const|let|var)\s+(\w+)\s*=\s*([^;\n]+)/g)].map(m=>[m[1],m]));
 if(file.endsWith('.go')) for(const match of source.matchAll(/\b(\w+)\s*\*\s*time\.(?:Nanosecond|Microsecond|Millisecond|Second|Minute|Hour)\b/g)) {
  const declaration=declarations.get(match[1]);
  if(declaration&&numeric.test(declaration[2])) add(declaration.index,declaration[0]);
 }
 for(const match of source.matchAll(/\b(setTimeout|setInterval|AbortSignal\.timeout|\.settimeout)\s*\(/g)) {
  const args=argsAt(source,match.index+match[0].length);
  const duration=(args[match[1].startsWith('set')?1:0]??'').trim();
  if(numeric.test(duration)) add(match.index,match[0]+args.join(',')+')');
  else if(/^\w+$/.test(duration)&&declarations.has(duration)) {
   const declaration=declarations.get(duration);
   if(numeric.test(declaration[2])) add(declaration.index,declaration[0]);
  }
 }
 return [...clocks.values()];
}

export function violations(file, source, allowlist=[]) {
 return literalClocks(file,source).filter(clock=>!allowlist.some(rule=>
  rule.file===file&&rule.statement===clock.statement&&rule.class==='B'&&rule.reason?.trim()));
}

export function checkRepository(directory=root) {
 const spec=JSON.parse(readFileSync(resolve(directory,'spec/protocol/v1/timeouts.json'),'utf8'));
 const allowance=spec.request.default_s;
 const read=path=>readFileSync(resolve(directory,path),'utf8');
 const failures=[];
 // Import-free browser factories and deployment-only Python scripts carry
 // generated literals. Check every copy against the same wire contract.
 const generated=[
  ['internal/runtimebudget/budget.go',/const Operation = (\d+) \* time.Minute/, allowance/60],
  ['ts/src/context.ts',/DEFAULT_REQUEST_TIMEOUT=(\d+)/, allowance*1000],
  ['ts/src/dsm/session.js',/finish\(undefined\); }, (\d+)\)/,allowance*1000+spec.browser.margin_ms],
  ['ops/private-ci/runtime_budget.py',/OPERATION_SECONDS = (\d+)/,allowance],
  ['node/src/shared-folder.ts',/const OPERATION_TIMEOUT_MS = (\d+)/,allowance*1000],
  ['python/synopkgland_app/shared_folder.py',/OPERATION_SECONDS = (\d+)/,allowance],
 ];
 for(const [file,pattern,want] of generated) if(Number(read(file).match(pattern)?.[1])!==want) failures.push(`${file}: generated budget differs from timeout spec`);
 for(const key of ['op_s','webapi_raw_execution_s','root_run_program_default_s','start_step_s']) if(spec.executor[key]!==allowance) failures.push(`timeouts.json: executor.${key} is not the shared budget`);
 const allowlist=JSON.parse(read('scripts/timeout-allowlist.json'));
 if(/time\.NewTimer\(limits\.timeout\(\)\)/.test(read('executor/root_jobs.go'))) failures.push('executor/root_jobs.go: a live job cannot have a total elapsed timer');
 const generatedFiles=new Map(generated.map(([file,pattern])=>[file,pattern]));
 const files=execFileSync('git',['ls-files','--cached','--others','--exclude-standard','-z'],{cwd:directory,encoding:'utf8'}).split('\0').filter(Boolean);
 for(const file of new Set(files)) {
  if(file.startsWith('internal/runtimebudget/')) continue;
  for(const clock of violations(file,read(file),allowlist)) {
   const generatedPattern=generatedFiles.get(file);
   if(generatedPattern?.test(clock.statement)) continue;
   failures.push(`${file}:${clock.line}: unaudited clock: ${clock.statement}`);
  }
 }
 return failures;
}

if(process.argv[1]&&import.meta.url===pathToFileURL(resolve(process.argv[1])).href) {
 const failures=checkRepository(process.argv[2]||root);
 if(failures.length) {console.error(failures.join('\n'));process.exitCode=1;}
 else console.log('timeout policy: one operation default; non-failure clocks reviewed');
}
