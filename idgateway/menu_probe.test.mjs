import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
// menu_page_test.go writes the launcher's probe script, a Go constant, here.
const source=readFileSync(process.env.SPL_MENU_PROBE,'utf8');
const target='https://app.example.com/#/home';
function launch(respond, reducedMotion) {
 const data={state:'start',mark:'x',ping:'https://app.example.com/_spl/ping',package:'App',target};
 const opening={hidden:false}, unreachable={hidden:true}, replaced=[], timers=[], requests=[];
 const context={
  document:{documentElement:{dataset:data},getElementById:id=>id==='opening'?opening:unreachable},
  location:{replace:v=>replaced.push(v)},
  setTimeout:(f,ms)=>timers.push({f,ms})-1,clearTimeout:i=>{if(timers[i])timers[i].cleared=true},
  AbortController:class{constructor(){this.signal={aborted:false}}abort(){this.signal.aborted=true;this.signal.onabort?.()}},
  fetch:(url,init)=>{requests.push({url,init});return new Promise((resolve,reject)=>{init.signal.onabort=()=>reject(new Error('aborted'));respond(resolve,reject)})},
 };
 if(reducedMotion!==undefined)context.matchMedia=q=>({matches:q==='(prefers-reduced-motion: reduce)'&&reducedMotion});
 vm.runInNewContext(source,context);
 const settle=()=>new Promise(r=>setImmediate(r));
 const fire=ms=>timers.filter(t=>t.ms===ms&&!t.cleared).forEach(t=>t.f());
 return {data,opening,unreachable,replaced,timers,requests,settle,fire};
}
const ok=body=>resolve=>resolve({ok:true,json:async()=>body});
test('the page shows the check at once, without credentials',async()=>{
 const p=launch(()=>{});
 assert.equal(p.data.state,'checking');
 assert.equal(p.opening.hidden,false);
 assert.equal(p.requests[0].url,p.data.ping);
 assert.deepEqual({...p.requests[0].init,signal:undefined},{mode:'cors',credentials:'omit',cache:'no-store',redirect:'error',signal:undefined});
});
test('a reachable App shows success, then opens, and the timer is stopped',async()=>{
 const p=launch(ok({package:'App'}));
 await p.settle();
 assert.equal(p.data.state,'ok');
 assert.ok(p.timers.find(t=>t.ms===8000).cleared);
 assert.deepEqual(p.replaced,[]);
 p.fire(400);
 assert.deepEqual(p.replaced,[target]);
 assert.equal(p.unreachable.hidden,true);
});
test('reduced motion opens without the success pause',async()=>{
 for(const [reduce,ms] of [[true,0],[false,400]]){
  const p=launch(ok({package:'App'}),reduce);
  await p.settle();
  p.fire(ms);
  assert.deepEqual(p.replaced,[target]);
 }
});
test('another package, an error status or a network failure shows the explanation',async()=>{
 for(const respond of [ok({package:'Other'}),ok(null),resolve=>resolve({ok:false}),(_r,reject)=>reject(new TypeError('blocked')),resolve=>resolve({ok:true,json:async()=>{throw new SyntaxError('html')}})]){
  const p=launch(respond);
  await p.settle();
  p.fire(400);
  assert.deepEqual(p.replaced,[]);
  assert.equal(p.data.state,'fail');
  assert.equal(p.data.mark,'x');
  assert.equal(p.unreachable.hidden,false);
  assert.equal(p.opening.hidden,true);
  assert.ok(p.timers.find(t=>t.ms===8000).cleared);
 }
});
test('a slow App waits 8 seconds before giving up',async()=>{
 const p=launch(()=>{});
 assert.deepEqual(p.timers.map(t=>t.ms),[8000]);
 await p.settle();
 assert.equal(p.data.state,'checking');
 p.fire(8000);
 await p.settle();
 assert.deepEqual(p.replaced,[]);
 assert.equal(p.data.state,'fail');
 assert.equal(p.unreachable.hidden,false);
 assert.equal(p.opening.hidden,true);
});
