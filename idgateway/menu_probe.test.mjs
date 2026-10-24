import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
// menu_page_test.go writes the launcher's probe script, a Go constant, here.
const source=readFileSync(process.env.SPL_MENU_PROBE,'utf8');
const data={ping:'https://app.example.com/_spl/ping',package:'App',target:'https://app.example.com/#/home'};
function launch(respond) {
 const opening={hidden:true}, unreachable={hidden:true}, replaced=[], timers=[], requests=[];
 const context={
  document:{documentElement:{dataset:data},getElementById:id=>id==='opening'?opening:unreachable},
  location:{replace:v=>replaced.push(v)},
  setTimeout:(f,ms)=>timers.push({f,ms})-1,clearTimeout:i=>{if(timers[i])timers[i].cleared=true},
  AbortController:class{constructor(){this.signal={aborted:false}}abort(){this.signal.aborted=true;this.signal.onabort?.()}},
  fetch:(url,init)=>{requests.push({url,init});return new Promise((resolve,reject)=>{init.signal.onabort=()=>reject(new Error('aborted'));respond(resolve,reject)})},
 };
 vm.runInNewContext(source,context);
 const settle=()=>new Promise(r=>setImmediate(r));
 const fire=ms=>timers.filter(t=>t.ms===ms&&!t.cleared).forEach(t=>t.f());
 return {opening,unreachable,replaced,timers,requests,settle,fire};
}
const ok=body=>resolve=>resolve({ok:true,json:async()=>body});
test('a reachable App opens without credentials and the timer is stopped',async()=>{
 const p=launch(ok({package:'App'}));
 await p.settle();
 assert.deepEqual(p.replaced,[data.target]);
 assert.equal(p.requests[0].url,data.ping);
 assert.deepEqual({...p.requests[0].init,signal:undefined},{mode:'cors',credentials:'omit',cache:'no-store',redirect:'error',signal:undefined});
 assert.ok(p.timers.find(t=>t.ms===8000).cleared);
 assert.equal(p.unreachable.hidden,true);
});
test('another package, an error status or a network failure shows the explanation',async()=>{
 for(const respond of [ok({package:'Other'}),resolve=>resolve({ok:false}),(_r,reject)=>reject(new TypeError('blocked'))]){
  const p=launch(respond);
  await p.settle();
  assert.deepEqual(p.replaced,[]);
  assert.equal(p.unreachable.hidden,false);
  assert.equal(p.opening.hidden,true);
 }
});
test('a slow App waits 8 seconds before giving up',async()=>{
 const p=launch(()=>{});
 p.fire(300);
 assert.equal(p.opening.hidden,false);
 assert.ok(p.timers.some(t=>t.ms===8000));
 assert.ok(p.timers.every(t=>t.ms===300||t.ms>=8000));
 p.fire(8000);
 await p.settle();
 assert.deepEqual(p.replaced,[]);
 assert.equal(p.unreachable.hidden,false);
 assert.equal(p.opening.hidden,true);
});
