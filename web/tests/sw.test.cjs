const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

const marker = '<meta name="application-name" content="Portfolio Tracker Offline">';

function worker(fetchImpl = async () => new Response(marker, { headers: { 'content-type': 'text/html' } })) {
  const listeners = {};
  const stores = new Map();
  const timers = new Map();
  let timerId = 0;
  const caches = {
    async open(name) {
      if (!stores.has(name)) stores.set(name, new Map());
      const store = stores.get(name);
      return {
        async put(key, value) { store.set(key, value); },
        async match(key) { return store.get(key)?.clone(); }
      };
    },
    async keys() { return [...stores.keys()]; },
    async delete(name) { return stores.delete(name); }
  };
  const self = {
    location: { origin: 'https://portfolio.test' },
    clients: { async claim() {} },
    addEventListener(type, fn) { listeners[type] = fn; }
  };
  const context = { self, caches, URL, Response, fetch: (...args) => fetchImpl(...args),
    setTimeout(fn) { const id = ++timerId; timers.set(id, fn); return id; },
    clearTimeout(id) { timers.delete(id); } };
  vm.runInNewContext(fs.readFileSync('web/static/sw.js', 'utf8'), context);
  return {
    stores, timers,
    async lifecycle(type) { let pending; listeners[type]({ waitUntil(value) { pending = value; } }); return pending; },
    navigation(path, options = {}) {
      let pending;
      listeners.fetch({ request: { method: 'GET', mode: 'navigate', url: new URL(path, self.location.origin).href, ...options },
        respondWith(value) { pending = value; } });
      return pending;
    }
  };
}

test('install validates and caches only the intended offline document', async () => {
  let args;
  const w = worker(async (...values) => {
    args = values;
    return new Response(marker, { headers: { 'content-type': 'text/html' } });
  });
  await w.lifecycle('install');
  assert.equal(args[0], '/static/offline.html');
  assert.equal(args[1].redirect, 'error');
  assert.equal(await (await w.navigation('/static/offline.html')).text(), marker);
  for (const response of [
    new Response('login', { headers: { 'content-type': 'text/html' } }),
    new Response(marker, { headers: { 'content-type': 'text/plain' } }),
    new Response(marker, { status: 503, headers: { 'content-type': 'text/html' } })
  ]) {
    const invalid = worker(async () => response);
    await assert.rejects(invalid.lifecycle('install'), /Invalid offline page/);
    assert.equal(invalid.stores.size, 0);
  }
  await assert.rejects(worker(async () => { throw new TypeError('redirect'); }).lifecycle('install'));
});

test('activation deletes only old portfolio offline caches', async () => {
  const w = worker();
  for (const name of ['portfolio-tracker-offline-v1', 'other-app']) w.stores.set(name, new Map());
  await w.lifecycle('install');
  await w.lifecycle('activate');
  assert.deepEqual([...w.stores.keys()].sort(), ['other-app', 'portfolio-tracker-offline-v2']);
});

test('page and offline-link navigations preserve all reachable server responses', async () => {
  let response = new Response('online');
  const w = worker(async url => typeof url === 'string' ? new Response(marker, { headers: { 'content-type': 'text/html' } }) : response);
  await w.lifecycle('install');
  assert.equal(await (await w.navigation('/holdings?sort=name')).text(), 'online');
  response = new Response('unauthorized', { status: 401 });
  assert.equal((await w.navigation('/settings')).status, 401);
  response = new Response('forbidden', { status: 403 });
  assert.equal((await w.navigation('/')).status, 403);
  response = Response.redirect('https://login.test/');
  assert.equal((await w.navigation('/')).headers.get('Location'), 'https://login.test/');
  response = new Response('Editable holdings', { status: 503 });
  assert.equal(await (await w.navigation('/holdings')).text(), 'Editable holdings');
  response = new Response('failure', { status: 500 });
  assert.equal(await (await w.navigation('/history')).text(), 'failure');
  assert.deepEqual([...w.stores.values()].map(store => [...store.keys()]), [['/static/offline.html']]);
});

test('opening the offline link still honors a reachable authentication proxy', async () => {
  const w = worker(async url => typeof url === 'string' ?
    new Response(marker, { headers: { 'content-type': 'text/html' } }) :
    new Response('forbidden', { status: 403 }));
  await w.lifecycle('install');
  assert.equal((await w.navigation('/static/offline.html')).status, 403);
});

test('navigation falls back on network rejection and awaits a slow authentication response', async () => {

  const offline = worker(async url => {
    if (typeof url === 'string') return new Response(marker, { headers: { 'content-type': 'text/html' } });
    throw new TypeError('offline');
  });
  await offline.lifecycle('install');
  assert.equal(await (await offline.navigation('/sources/')).text(), marker);
  assert.equal(await (await offline.navigation('/static/offline.html')).text(), marker);
  let respond;
  const slow = worker(async url => typeof url === 'string' ?
    new Response(marker, { headers: { 'content-type': 'text/html' } }) :
    new Promise(resolve => { respond = resolve; }));
  await slow.lifecycle('install');
  const pending = slow.navigation('/');
  for (const timer of slow.timers.values()) timer();
  respond(new Response('unauthorized', { status: 401 }));
  assert.equal((await pending).status, 401);
});

test('ignores non-page requests, cross-origin requests and mutations', () => {
  const w = worker();
  for (const path of ['/api/holdings', '/partials/holding-form', '/static/style.css', '/export', '/unknown', 'https://other.test/']) {
    assert.equal(w.navigation(path), undefined, path);
  }
  assert.equal(w.navigation('/', { method: 'POST' }), undefined);
  assert.equal(w.navigation('/', { mode: 'cors' }), undefined);
});
