const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

const snapshotKey = 'portfolio-offline-snapshot-v1';
const generationKey = 'portfolio-offline-min-generation';

function loadPage(fetch, { editing = false, generation = '4', storage = new Map() } = {}) {
  const events = {};
  const destinations = [];
  const notice = { hidden: true };
  let timeout;
  const documentEvents = {};
  const document = {
    documentElement: { dataset: { portfolioGeneration: generation } },
    readyState: 'loading',
    visibilityState: 'visible',
    getElementById: () => notice,
    querySelector: () => null,
    addEventListener(type, fn) { documentEvents[type] = fn; }
  };
  const window = {
    PortfolioApp: { hasPendingEdits: () => editing },
    addEventListener(type, fn) { events[type] = fn; }
  };
  const navigator = { onLine: true, serviceWorker: { controller: {}, async register() {} } };
  const localStorage = {
    getItem: key => storage.get(key) ?? null,
    setItem: (key, value) => storage.set(key, String(value)),
    removeItem: key => storage.delete(key)
  };
  window.localStorage = localStorage;
  const context = vm.createContext({
    document, window, navigator, fetch, AbortController,
    localStorage,
    location: { replace(url) { destinations.push(url); } },
    setTimeout(fn) { timeout = fn; }, clearTimeout() {}, setInterval() {}
  });
  vm.runInContext(fs.readFileSync('web/static/app.js', 'utf8'), context);
  window.PortfolioApp = { hasPendingEdits: () => editing };
  vm.runInContext(fs.readFileSync('web/static/pwa.js', 'utf8'), context);
  return { events, destinations, notice, capture: documentEvents.DOMContentLoaded, expire() { timeout(); } };
}

test('a reachable HTTP error does not make a live tab show saved data', async () => {
  const page = loadPage(async () => new Response('Proxy unavailable', { status: 503 }));
  await page.events.focus();
  assert.deepEqual(page.destinations, []);
});

test('an aborted connectivity check does not navigate away from the live tab', async () => {
  const page = loadPage((_, { signal }) => new Promise((_, reject) => {
    signal.addEventListener('abort', () => reject(signal.reason));
  }));
  const check = page.events.focus();
  page.expire();
  await check;
  assert.deepEqual(page.destinations, []);
});

test('a failed connection opens the offline link without abandoning an edit', async () => {
  for (const editing of [false, true]) {
    const page = loadPage(async () => { throw new TypeError('network failure'); }, { editing });
    await page.events.focus();
    assert.deepEqual(page.destinations, editing ? [] : ['/static/offline.html']);
    assert.equal(page.notice.hidden, !editing);
  }
});

test('a live tab discards older data after a reset from another device', async () => {
  const storage = new Map([[snapshotKey, JSON.stringify({ generation: '4', overview: {} })]]);
  const page = loadPage(async () => new Response('{}', { headers: { 'X-Portfolio-Generation': '5' } }), { storage });
  await page.events.focus();
  assert.equal(storage.has(snapshotKey), false);
  assert.equal(storage.get(generationKey), '5');
  page.capture();
  assert.equal(storage.has(snapshotKey), false, 'a delayed pre-reset page must not recreate the snapshot');
  loadPage(async () => {}, { storage, generation: '5' }).capture();
  assert.equal(JSON.parse(storage.get(snapshotKey)).generation, '5');
});

test('a live tab drops its saved data when its connection check is denied or redirected', async () => {
  for (const response of [new Response(null, { status: 401 }), new Response(null, { status: 403 }),
    { status: 0, type: 'opaqueredirect' }]) {
    const storage = new Map([[snapshotKey, JSON.stringify({ generation: '4', overview: {} })]]);
    const page = loadPage(async () => response, { storage });
    await page.events.focus();
    assert.equal(storage.has(snapshotKey), false);
    assert.deepEqual(page.destinations, []);
  }
});

test('the offline view ignores pre-reset data left by an older tab', () => {
  const script = fs.readFileSync('web/static/offline.html', 'utf8').match(/<script>([\s\S]*)<\/script>/)[1];
  const storage = new Map([[snapshotKey, JSON.stringify({ generation: '4', overview: { current: '€1.234,56' } })]]);
  const events = {};
  function element() {
    return {
      children: [],
      replaceChildren() { this.children = []; },
      appendChild(child) { this.children.push(child); }
    };
  }
  const elements = { content: element(), empty: element() };
  vm.runInNewContext(script, {
    document: { createElement: element, getElementById: id => elements[id] },
    window: { addEventListener(type, fn) { events[type] = fn; } },
    localStorage: { getItem: key => storage.get(key) ?? null }
  });
  events.pageshow();
  assert.equal(elements.empty.hidden, true);
  assert.equal(elements.content.children[0].children[2].children[0].children[0].textContent, '€1.234,56');
  storage.set(generationKey, '5');
  events.storage({ key: generationKey });
  assert.equal(elements.empty.hidden, false);
  assert.deepEqual(elements.content.children, []);
});
