'use strict';
// Tab-local navigation state only. The upstream owns all account/session state.
const key = 'synopkgland.oidc.browser' + (cfg.ingress_path || '');
const ttl = 120000;
const here = () => location.pathname + location.search + location.hash;
const login = p => cfg.login_paths.includes(p);
const callback = u => cfg.callback_paths.includes(u.pathname) ||
  (login(u.pathname) && cfg.completion_query && u.searchParams.has(cfg.completion_query));
function safe(value) {
  if (typeof value !== 'string' || value.length > 4096 || !value.startsWith('/') || value.startsWith('//') || /[\\\s]/.test(value)) return cfg.ingress_path || '/';
  try {
    const path = decodeURIComponent(value.split(/[?#]/)[0]);
    if (path.includes('%') || path.includes('\\') || path.startsWith('//') || path.split('/').some(p => p === '.' || p === '..')) return cfg.ingress_path || '/';
    const u = new URL(value, location.origin);
    if (u.origin !== location.origin || (cfg.ingress_path && !u.pathname.startsWith(cfg.ingress_path)) || u.pathname.startsWith((cfg.ingress_path || '/') + '_sso') || login(u.pathname) || callback(u) || u.pathname === new URL(cfg.start, location.origin).pathname) return cfg.ingress_path || '/';
    return u.pathname + u.search + u.hash;
  } catch (_) { return cfg.ingress_path || '/'; }
}
let state;
function save(value) { sessionStorage.setItem(key, JSON.stringify(value)); state = value; }
try {
  state = JSON.parse(sessionStorage.getItem(key) || 'null');
  if (!state || Date.now() - state.time > ttl || Date.now() < state.time) state = null;
} catch (_) { state = null; }
let previous = here();
function begin(target) {
  if (state && state.phase === 'pending') return; // A failed native flow stays visible, without a loop.
  try { save({phase:'pending', target:safe(target), time:Date.now()}); }
  catch (_) { return; } // Storage blocked: retain the native login instead of looping.
  location.replace(cfg.start);
}
function finish() {
  const target = safe(state.target);
  save({phase:'resume', target, time:Date.now()});
  location.replace(target);
}
const current = new URL(location.href);
if (callback(current)) {
  // Let the native callback consume its own response before restoring the page.
} else if (state && state.phase === 'resume' && safe(here()) === state.target) {
  sessionStorage.removeItem(key); state = null;
} else {
  if (state && state.phase === 'pending') {
    // A server-side callback may return directly to the app's home page.
    finish();
  } else {
    begin(login(current.pathname) ? '/' : here());
  }
}
function changed() {
  const next = new URL(location.href);
  if (callback(next)) return;
  if (state && state.phase === 'pending') {
    if (!login(next.pathname)) finish();
  } else if (login(next.pathname)) {
    begin(previous);
  } else { previous = here(); }
}
for (const name of ['pushState', 'replaceState']) {
  const original = history[name];
  history[name] = function(...args) { const result = original.apply(this,args); changed(); return result; };
}
addEventListener('popstate', changed);
