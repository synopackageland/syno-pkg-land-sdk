import {readFileSync} from 'node:fs'
import {test} from 'vitest'
import assert from 'node:assert/strict'
import {AUTHORIZATION_RUNTIME, ROOT_RUNTIME} from '../native'
import * as native from '../native'
// Native products such as Reclaim render both runtimes into their own DSM window.
test('the native entry exports the authorization and root runtimes; the Vue gate ships in @synopackageland/sdk/vue', () => {
 assert.deepEqual(Object.keys(native).sort(),['AUTHORIZATION_RUNTIME','ROOT_RUNTIME']);
 assert.match(AUTHORIZATION_RUNTIME,/function createAuthorizationAccess/);
 assert.match(ROOT_RUNTIME,/function createRootAccess/);
});
test('native-layout withdrawal must complete before removing tasks; failures abort', async () => {
 const events:string[]=[];
 const factory=new Function(AUTHORIZATION_RUNTIME+';return createAuthorizationAccess;')();
 const options={tasks:[{op:'bootstrap'}],root:{remove:async()=>{events.push('remove')}},call:async(method:string)=>{events.push(method);return method.endsWith('status')?{command:'authorization',schema_version:1,state:'authorization_required'}:{command:'authorization',schema_version:1,phase:'plan',operations:['bootstrap'],withdraw:true,disable:false,run:false}},withdraw:async()=>{events.push('withdraw')}};
 await factory(options).revoke();
 assert.deepEqual(events,['authorization.revoke','withdraw','remove','authorization.status']);
 events.length=0;
 await assert.rejects(factory({...options,withdraw:async()=>{throw Error('failed')}}).revoke(),/failed/);
 assert.deepEqual(events,['authorization.revoke']);
 await assert.rejects(factory({...options,withdraw:undefined}).revoke(),/authorization_bad_response/);
});

function nativeRoot(script = '# synopkgland-root Product.bootstrap\npin=' + 'a'.repeat(64)) {
 const task = {op:'bootstrap',name:'Product Bootstrap',header:'# synopkgland-root Product.bootstrap\n',script,output:false};
 const stored:any = {id:1,name:task.name,owner:'root',real_owner:'root',enable:false,type:'script',extra:{script}};
 const events:string[]=[]; let admin=true, runs=0;
 const root = new Function(ROOT_RUNTIME+';return createRootAccess;')()({
  tasks:[task],user:()=>({name:'admin',admin}),call:async()=>{throw Error('no protected output')},
  confirmPassword:async()=>({confirmed:true,token:'fresh-one-use-token'}),wait:async()=>{},
  send:(api:string,method:string,version:number,params:any)=>send(api,method,version,params)
 });
 let send=async(_api:string,method:string,_version:number,params:any):Promise<any>=>{
   events.push(method);
   if(method==='list')return {tasks: stored.id?[stored]:[],total:stored.id?1:0};
   if(method==='get')return structuredClone(stored);
   if(method==='delete'){stored.id=0;return {}};
   if(method==='create'){
    assert.equal(params.SynoConfirmPWToken,'fresh-one-use-token');assert.equal(params.enable,false);
    stored.id=2;stored.extra.script=params.extra.script;return {id:2};
   }
   if(method==='run'){runs++;return {}}
   if(method==='get_history_status_list')return runs?[{timestamp:runs,exit_type:'normal',stop_time:'2026-10-10 00:00:00',exit_code:0}]:[];
   throw Error('unexpected '+method);
 };
 const original=send;
 return {root,stored,events,send:original,setSend:(next:typeof send)=>{send=next},setAdmin:(value:boolean)=>{admin=value}};
}

test('native adapter never runs stale digest tasks and requires fresh approval to replace them', async()=>{
 const f=nativeRoot();f.stored.extra.script=f.stored.extra.script.replace('a'.repeat(64),'b'.repeat(64));
 assert.equal(await f.root.state('bootstrap'),'outdated');
 await assert.rejects(f.root.run('bootstrap'),{code:'root_not_ready'});
 assert.equal(f.events.includes('run'),false);
 assert.equal(await f.root.authorize('bootstrap'),'ready');
 assert.equal(f.events.filter(e=>e==='create').length,1);
 assert.equal(f.events.filter(e=>e==='delete').length,1);
});

test('native adapter refuses foreign task identity and non-admin approval without mutation', async()=>{
 const f=nativeRoot();f.stored.extra.script='foreign';
 await assert.rejects(f.root.authorize('bootstrap'),{code:'root_conflict'});
 await assert.rejects(f.root.remove('bootstrap'),{code:'root_conflict'});
 f.setAdmin(false);
 await assert.rejects(f.root.authorize('bootstrap'),{code:'admin_required'});
 assert.equal(f.events.some(e=>['create','delete','run'].includes(e)),false);
});

test('native authorization runs the pinned bootstrap for every requested build and trusts backend status',async()=>{
 const events:string[]=[];
 const factory=new Function(AUTHORIZATION_RUNTIME+';return createAuthorizationAccess;')();
 const adapter=factory({tasks:[{op:'bootstrap'}],root:{authorize:async()=>{events.push('approve');return 'ready'},run:async()=>{events.push('bootstrap')}},call:async(method:string)=>{
  if(method.endsWith('status'))return {command:'authorization',schema_version:1,state:'authorization_required'};
  return {command:'authorization',schema_version:1,phase:'plan',operations:['bootstrap'],run:true};
 }});
 for(let build=1;build<=2;build++)assert.equal((await adapter.request()).state,'authorization_required');
 assert.deepEqual(events,['approve','bootstrap','approve','bootstrap']);
});

test('a reason-only upgrade tombstone can never read as authorized',async()=>{
 const factory=new Function(AUTHORIZATION_RUNTIME+';return createAuthorizationAccess;')();
 const events:string[]=[];
 const access=(status:object)=>factory({tasks:[{op:'bootstrap'}],root:{authorize:async()=>{events.push('approve');return 'ready'},run:async()=>{events.push('bootstrap')}},call:async(method:string)=>method.endsWith('status')?{command:'authorization',schema_version:1,...status}:{command:'authorization',schema_version:1,phase:'plan',operations:['bootstrap'],run:true}});
 for(const state of ['ready','not_admin','session_expired','unavailable'])
  await assert.rejects(access({state,reason:'upgrade_reapproval'}).status(),{code:'authorization_bad_response'},state);
 const pending=access({state:'authorization_required',reason:'upgrade_reapproval'});
 assert.equal((await pending.status()).reason,'upgrade_reapproval');
 assert.equal((await pending.request()).state,'authorization_required');
 assert.deepEqual(events,['approve','bootstrap']);
});

// under IO load the bootstrap task ran 61 s to 4 min 36 s, but the
// window gave up after 30 s (root_timeout) although the grant landed later.
function runningRoot(runMs: number, exitCode = 0) {
 const script = '# synopkgland-root Product.bootstrap\npin=' + 'a'.repeat(64);
 const task = {op:'bootstrap',name:'Product Bootstrap',header:'# synopkgland-root Product.bootstrap\n',script,output:false};
 const stored = {id:1,name:task.name,owner:'root',real_owner:'root',enable:false,type:'script',extra:{script}};
 let clock = 0, startedAt = -1;
 const progress:any[] = [];
 const root = new Function(ROOT_RUNTIME+';return createRootAccess;')()({
  tasks:[task],user:()=>({name:'admin',admin:true}),call:async()=>null,now:()=>clock,
  confirmPassword:async()=>({confirmed:true,token:'t'}),wait:async(ms:number)=>{clock+=ms},progress:(p:any)=>progress.push(p),
  send:async(_api:string,method:string)=>{
   if(method==='list')return {tasks:[stored],total:1};
   if(method==='get')return structuredClone(stored);
   if(method==='run'){startedAt=clock;return {}}
   if(method==='get_history_status_list'){
    if(startedAt<0)return [];
    // DSM records the run only after the scheduler picks it up.
    if(clock-startedAt<2000)return [];
    const done=clock-startedAt>=runMs;
    return [{timestamp:startedAt,exit_type:done?'normal':'running',stop_time:done?'2026-10-10 00:00:00':'',exit_code:done?exitCode:0}];
   }
   throw Error('unexpected '+method);
  }
 });
 return {root,progress,elapsed:()=>clock};
}
test('a root task that is still running keeps the window waiting and reports progress', async () => {
 const f = runningRoot(276000);
 await f.root.run('bootstrap');
 assert.ok(f.elapsed() >= 276000);
 assert.ok(f.progress.some(p => p.op === 'bootstrap' && p.state === 'running' && p.elapsedMs >= 60000), JSON.stringify(f.progress.slice(-1)));
 assert.ok(f.progress.some(p => p.state === 'queued'));
});
test('a root task that never finishes times out only after a load-sized limit', async () => {
 const f = runningRoot(Infinity);
 await assert.rejects(f.root.run('bootstrap'), {code:'root_timeout'});
 assert.ok(f.elapsed() >= 600000, String(f.elapsed()));
});
test('a finished root task that failed is reported at once', async () => {
 const f = runningRoot(3000, 1);
 await assert.rejects(f.root.run('bootstrap'), {code:'root_task_failed'});
 assert.ok(f.elapsed() < 10000);
});
test('authorization trusts a ready grant over a slow or signalled bootstrap task', async () => {
 const factory = new Function(AUTHORIZATION_RUNTIME+';return createAuthorizationAccess;')();
 for (const code of ['root_timeout','root_task_failed']) {
  let state = 'authorization_required';
  const access = factory({tasks:[{op:'bootstrap'}],root:{authorize:async()=>'ready',run:async()=>{state='ready';throw Object.assign(Error(code),{code})}},call:async(method:string)=>method.endsWith('status')?{command:'authorization',schema_version:1,state}:{command:'authorization',schema_version:1,phase:'plan',operations:['bootstrap'],run:true}});
  assert.equal((await access.request()).state, 'ready', code);
  state = 'authorization_required';
  const failing = factory({tasks:[{op:'bootstrap'}],root:{authorize:async()=>'ready',run:async()=>{throw Object.assign(Error(code),{code})}},call:async(method:string)=>method.endsWith('status')?{command:'authorization',schema_version:1,state}:{command:'authorization',schema_version:1,phase:'plan',operations:['bootstrap'],run:true}});
  await assert.rejects(failing.request(), {code}, code);
 }
});
test('DSM 7.1 waits for a slow root task through its view history as well', async () => {
 const script = '# synopkgland-root Product.bootstrap\npin=' + 'a'.repeat(64);
 const task = {op:'bootstrap',name:'Product Bootstrap',header:'# synopkgland-root Product.bootstrap\n',script,output:false};
 const stored = {id:1,name:task.name,owner:'root',real_owner:'root',enable:false,type:'script',extra:{script}};
 let clock = 0, startedAt = -1;
 const stamp = (ms:number) => new Date(Date.UTC(2026,9,10)+ms).toISOString().slice(0,19).replace('T',' ');
 const root = new Function(ROOT_RUNTIME+';return createRootAccess;')()({
  tasks:[task],user:()=>({name:'admin',admin:true}),call:async()=>null,now:()=>clock,
  confirmPassword:async()=>({confirmed:true}),wait:async(ms:number)=>{clock+=ms},
  send:async(_api:string,method:string,version:number)=>{
   if(method==='list'){if(version===3)throw {code:'dsm_104'};return {tasks:[stored],total:1}}
   if(method==='get')return structuredClone(stored);
   if(method==='run'){startedAt=clock;return {}}
   if(method==='view'){
    if(startedAt<0)return undefined;
    const done=clock-startedAt>=150000;
    return [{start_time:stamp(startedAt),stop_time:done?stamp(clock):'',exit_type:done?'normal':'running',exit_code:0}];
   }
   throw Error('unexpected '+method);
  }
 });
 await root.run('bootstrap');
 assert.ok(clock >= 150000);
});
// authorize then run took about 50 s; run read the task again right
// after authorize had verified it.
test('run right after authorize reuses the task authorize just verified', async () => {
 const f=nativeRoot();f.stored.id=0;
 assert.equal(await f.root.authorize('bootstrap'),'ready');
 await f.root.run('bootstrap');
 assert.deepEqual(f.events,['list','create','list','get','get_history_status_list','run','get_history_status_list']);
});
test('an already-authorized task runs without a second read', async () => {
 const f=nativeRoot();
 assert.equal(await f.root.authorize('bootstrap'),'ready');
 await f.root.run('bootstrap');
 assert.deepEqual(f.events,['list','get','get_history_status_list','run','get_history_status_list']);
});
test('the verified task is reused once; a later run or a removal reads DSM again', async () => {
 const f=nativeRoot();
 await f.root.authorize('bootstrap');
 await f.root.run('bootstrap');
 f.events.length=0;
 await f.root.run('bootstrap');
 assert.deepEqual(f.events.slice(0,2),['get','get_history_status_list']);
 await f.root.authorize('bootstrap');
 assert.equal(await f.root.remove('bootstrap'),'missing');
 f.events.length=0;
 await assert.rejects(f.root.run('bootstrap'),{code:'root_not_ready'});
 assert.deepEqual(f.events,['list']);
});
test('a root task is polled at most once a second', async () => {
 const f = runningRoot(60000);
 await f.root.run('bootstrap');
 assert.ok(f.elapsed() <= 61000, String(f.elapsed()));
});

// spec/scheduler/v1/readback.json: a native product's task is a Task Scheduler
// task like the executor's control task, so the control cases apply; the
// native runtime has no EventScheduler task, so the boot cases do not.
const readback=JSON.parse(readFileSync(new URL('../../../spec/scheduler/v1/readback.json',import.meta.url),'utf8'))
const verdicts:Record<string,string>={ready:'ready',task_missing:'missing',task_duplicate:'conflict',task_unverified:'conflict',task_outdated:'outdated',task_conflict:'conflict'}
for(const c of readback.cases.filter((c:any)=>c.kind==='control')){
 test(`scheduler readback vector: ${c.name}`,async()=>{
  const header=`# synopkgland-root ${readback.package}.bootstrap\n`
  const task={op:'bootstrap',name:`${readback.package} Bootstrap`,header,script:`${header}pin=${readback.policyDigest}`,output:false}
  const outdated=`${header}pin=${readback.outdatedPolicyDigest}`
  const fill=(value:unknown)=>value===undefined||value===null?undefined:JSON.parse(JSON.stringify(value)
   .replaceAll('@NAME@',JSON.stringify(task.name).slice(1,-1))
   .replaceAll('@OUTDATED@',JSON.stringify(outdated).slice(1,-1))
   .replaceAll('@SCRIPT@',JSON.stringify(task.script).slice(1,-1)))
  const list=fill(c.list)
  const root=new Function(ROOT_RUNTIME+';return createRootAccess;')()({tasks:[task],user:()=>({name:'admin',admin:true}),
   send:async(_api:string,method:string)=>method==='list'?{tasks:list,total:list.length}:fill(c.get)})
  assert.equal(await root.state('bootstrap'),verdicts[c.expect])
 })
}

// spec/scheduler/v1 Rules: a step lists once; a later step gets the listed id
// and lists again when that get answers 4801 or another task. A step that
// deletes lists afresh.
test('native adapter gets the listed id in later steps and lists again when it is gone or another task',async()=>{
 const f=nativeRoot()
 assert.equal(await f.root.state('bootstrap'),'ready')
 assert.deepEqual(f.events.splice(0),['list','get'])
 assert.equal(await f.root.state('bootstrap'),'ready')
 assert.deepEqual(f.events.splice(0),['get'])
 await f.root.run('bootstrap')
 assert.deepEqual(f.events.splice(0),['get','get_history_status_list','run','get_history_status_list'])
 f.stored.id=5
 assert.equal(await f.root.state('bootstrap'),'ready')
 assert.deepEqual(f.events.splice(0),['get','list','get'])
 assert.equal(await f.root.remove('bootstrap'),'missing')
 assert.deepEqual(f.events.splice(0),['list','get','delete','list'])
})

test('a cached get that DSM answers 4801 lists again',async()=>{
 const f=nativeRoot(); let gone=false
 const send=f.send
 f.setSend(async(api:string,method:string,version:number,params:any)=>{
  if(method==='get'&&gone){gone=false;f.events.push('get');throw Object.assign(Error('4801'),{code:'4801'})}
  return send(api,method,version,params)
 })
 assert.equal(await f.root.state('bootstrap'),'ready')
 gone=true;f.events.length=0
 assert.equal(await f.root.state('bootstrap'),'ready')
 assert.deepEqual(f.events,['get','list','get'])
})
