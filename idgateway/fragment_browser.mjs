// Optional real-browser regression driver; Node's built-in WebSocket needs no packages.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {setTimeout as delay} from 'node:timers/promises';

const [chrome, profile, start, expected, fallback] = process.argv.slice(2);
const child = spawn(chrome, ['--headless', '--no-sandbox', '--disable-gpu',
  '--no-first-run', '--disable-background-networking', '--no-proxy-server',
  '--disable-dev-shm-usage', '--remote-debugging-port=0', `--user-data-dir=${profile}`]);
let socket;
let send;
try {
  const endpoint = await new Promise((resolve, reject) => {
    let output = '';
    child.stderr.on('data', chunk => {
      output += chunk;
      const match = output.match(/DevTools listening on (ws:\/\/\S+)/);
      if (match) resolve(match[1]);
    });
    child.on('error', reject);
    child.on('exit', code => reject(new Error(`Chromium exited: ${code}`)));
  });
  socket = new WebSocket(endpoint);
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, {once: true});
    socket.addEventListener('error', reject, {once: true});
  });
  let next = 0;
  const pending = new Map();
  socket.addEventListener('message', ({data}) => {
    const m = JSON.parse(data);
    const p = pending.get(m.id);
    if (p) {
      pending.delete(m.id);
      m.error ? p.reject(new Error(m.error.message)) : p.resolve(m.result);
    }
  });
  send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    const id = ++next;
    pending.set(id, {resolve, reject});
    socket.send(JSON.stringify({id, method, params, sessionId}));
  });
  const {targetId} = await send('Target.createTarget', {url: 'about:blank'});
  const {sessionId} = await send('Target.attachToTarget', {targetId, flatten: true});
  await send('Page.navigate', {url: start}, sessionId);
  let actual;
  for (let i = 0; i < 200; i++) {
    if (fallback) await send('Runtime.evaluate', {
      expression: `(()=>{const box=document.getElementById('unreachable');if(box&&!box.hidden)box.querySelector('a.button').click()})()`,
    }, sessionId);
    const {result} = await send('Runtime.evaluate', {
      expression: `document.getElementById('result')?.textContent`, returnByValue: true,
    }, sessionId);
    actual = result.value;
    if (actual) break;
    await delay(50);
  }
  assert.equal(actual, expected);
  console.log(`Deep link preserved: ${actual}`);
} finally {
  if (socket?.readyState === WebSocket.OPEN) {
    await Promise.race([send('Browser.close').catch(() => {}), delay(2000, undefined, {ref: false})]);
  }
  socket?.close();
  const exited = child.exitCode !== null || child.signalCode !== null
    ? Promise.resolve() : new Promise(resolve => child.once('exit', resolve));
  child.kill();
  await exited;
}
