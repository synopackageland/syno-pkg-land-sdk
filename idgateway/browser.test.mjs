import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
const source=readFileSync(new URL('./browser.js',import.meta.url),'utf8');
const cfg={start:'/native/sso?method=login',login_paths:['/login'],callback_paths:['/callback'],completion_query:'token'};
function document(path,storage=new Map(),config=cfg) {
 let url=new URL(path,'https://app.example:8443'), redirects=[];
 const location={get pathname(){return url.pathname},get search(){return url.search},get hash(){return url.hash},get origin(){return url.origin},get href(){return url.href},replace(v){redirects.push(v)}};
 const history={pushState(_s,_t,v){url=new URL(v,url)},replaceState(_s,_t,v){url=new URL(v,url)}};
 const context={cfg:config,location,history,URL,Date,sessionStorage:{getItem:k=>storage.get(k),setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)},addEventListener(){}};
 vm.runInNewContext(source,context);
 return {redirects,history,storage};
}
test('deep bookmark, native callback and SPA logout use native SSO without touching app state',()=>{
 let doc=document('/library?q=abc#files');
 assert.deepEqual(doc.redirects,[cfg.start]);
 doc=document('/login?token=UPSTREAM-PRIVATE',doc.storage);
 assert.deepEqual(doc.redirects,[]);
 doc.history.replaceState({},'','/');
 assert.deepEqual(doc.redirects,['/library?q=abc#files']);
 doc=document('/library?q=abc#files',doc.storage);
 assert.deepEqual(doc.redirects,[]);
 doc.history.pushState({},'','/login?redirect=%2Flibrary');
 assert.deepEqual(doc.redirects,[cfg.start]);
 const state=JSON.stringify([...doc.storage]);
 assert.ok(!state.includes('UPSTREAM-PRIVATE'));
});
test('server callback restores deep target once',()=>{
 let doc=document('/deep');
 doc=document('/',doc.storage);
 assert.deepEqual(doc.redirects,['/deep']);
 doc=document('/deep',doc.storage);
 assert.deepEqual(doc.redirects,[]);
});
test('foreign, ambiguous and encoded targets cannot become return destinations',()=>{
 for(const path of ['/a%2f..%2fb','/%2f%2fevil.example','/x%5cy','/_sso/authorize','/login?redirect=https://evil.example']){
  let doc=document(path);
  doc=document('/callback?code=example',doc.storage);
  doc.history.replaceState({},'','/');
  assert.deepEqual(doc.redirects,['/'],path);
 }
});
test('unsolicited callback does not initiate another login and pending failure does not loop',()=>{
 let doc=document('/callback?error=access_denied');
 assert.deepEqual(doc.redirects,[]);
 doc=document('/deep');
 doc=document('/login?error=denied',doc.storage);
 assert.deepEqual(doc.redirects,['/deep']);
 doc=document('/deep',doc.storage);
 assert.deepEqual(doc.redirects,[]);
});

test('path ingress namespaces browser storage and rejects sibling return targets',()=>{
 const prefix='/synopkgland/ExampleApp/';
 const scoped={start:prefix+'native/sso',login_paths:[prefix+'login'],callback_paths:[prefix+'callback'],ingress_path:prefix};
 const other={...scoped,ingress_path:'/synopkgland/OtherApp/',start:'/synopkgland/OtherApp/native/sso'};
 let doc=document(prefix+'library',new Map(),scoped);
 assert.deepEqual(doc.redirects,[scoped.start]);
 const sibling=document('/synopkgland/OtherApp/library',doc.storage,other);
 assert.deepEqual(sibling.redirects,[other.start]);
 assert.equal(doc.storage.size,2);
 for(const path of ['/synopkgland/OtherApp/secret',prefix+'_sso/authorize']) {
  let d=document(path,new Map(),scoped);
  d=document(prefix+'callback',d.storage,scoped);
  d.history.replaceState({},'',prefix);
  assert.deepEqual(d.redirects,[prefix]);
 }
});
