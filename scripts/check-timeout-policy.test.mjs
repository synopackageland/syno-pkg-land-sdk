import {test} from 'node:test';
import assert from 'node:assert/strict';
import {literalClocks, violations} from './check-timeout-policy.mjs';

test('new literal operation clocks fail across supported source languages',()=>{
 for(const [file,source] of [
  ['new.go','ctx, cancel := context.WithTimeout(ctx, 8*time.Second)'],
  ['new.go','const localBudget = 20 * time.Second'],
  ['new.go','const local = 8\ncontext.WithTimeout(ctx,local*time.Second)'],
  ['new.go','context.WithTimeout(ctx,time.Duration(8)*time.Second)'],
  ['new.ts','setTimeout(() => { abort() }, 8000)'],
  ['new.ts','const local = 8 * 1000; setTimeout(abort, local)'],
  ['new.ts','AbortSignal.timeout(8_000)'],
  ['new.ts','const timeoutMs=8000'],
  ['new.py','urlopen(req, timeout=20)'],
  ['new.py','deadline = time.monotonic() + 20'],
  ['new.sh','timeout=10'],
  ['compose.yaml','timeout: 5s'],
 ]) assert.ok(violations(file,source).length,`${file}: ${source}`);
});

test('shared operation values and caller-derived deadlines need no exception',()=>{
 for(const [file,source] of [
  ['new.go','context.WithTimeout(ctx, runtimebudget.Operation)'],
  ['new.go','context.WithTimeout(ctx, time.Duration(cfg.Seconds)*time.Second)'],
  ['new.ts','setTimeout(abort, clientTimeout())'],
  ['new.py','urlopen(req, timeout=OPERATION_SECONDS)'],
 ]) assert.deepEqual(violations(file,source),[]);
});

test('B exceptions pin the exact statement and require a reason',()=>{
 const file='poll.go',source='const pollInterval = 2 * time.Second';
 const statement=literalClocks(file,source)[0].statement;
 const rule={file,statement,class:'B',reason:'Poll spacing; does not end the operation'};
 assert.deepEqual(violations(file,source,[rule]),[]);
 assert.ok(violations(file,source.replace('2','3'),[rule]).length);
 assert.ok(violations(file,source,[{...rule,reason:''}]).length);
 assert.ok(violations(file,source,[{...rule,class:'A'}]).length);
});

test('test clocks do not become runtime policy exceptions',()=>{
 assert.deepEqual(literalClocks('slow_test.go','time.Sleep(60*time.Second)'),[]);
 assert.deepEqual(literalClocks('tests/slow.py','timeout=1'),[]);
});
