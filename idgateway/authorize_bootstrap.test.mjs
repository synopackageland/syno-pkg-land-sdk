import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';

const html = readFileSync(process.env.SPL_AUTHORIZE_BOOTSTRAP, 'utf8');
const source = html.match(/<script nonce="[^"]+">([\s\S]*?)<\/script>/)[1];
const action = html.match(/<form id="f"[^>]*action="([^"]*)"/)[1].replaceAll('&amp;', '&');

test('bootstrap keeps the normalized query and browser-only fragment in its POST navigation', async () => {
  for (const hash of ['', '#opened', '#/files?folder=a%2Fb', '#%E6%AA%94%E6%A1%88', '#https://foreign.example/']) {
    const current = new URL('https://dsm.example/authorize.cgi?resume=opaque' + hash);
    let target = new URL(action, current), submitted;
    const token = {value: ''}, requests = [];
    const form = {
      get action() { return target.href; },
      set action(value) { target = new URL(value, current); },
      submit() { submitted = {url: target.href, token: token.value}; },
    };
    vm.runInNewContext(source, {
      document: {getElementById: id => id === 'f' ? form : token},
      location: current,
      fetch: async (url, init) => {
        requests.push({url, init});
        return {json: async () => ({SynoToken: 'example-token'})};
      },
    });
    await new Promise(resolve => setImmediate(resolve));
    const expected = new URL(action, current);
    expected.hash = hash;
    assert.deepEqual(submitted, {url: expected.href, token: 'example-token'});
    assert.equal(target.origin, current.origin);
    assert.ok(target.searchParams.has('txn'));
    assert.ok(target.searchParams.has('return'));
    assert.deepEqual(requests.map(r => r.url), ['/webman/login.cgi?enable_syno_token=yes']);
  }
});
