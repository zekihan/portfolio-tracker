const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function loadPage(fetch, { editing = false } = {}) {
  const events = {};
  const destinations = [];
  const notice = { hidden: true };
  let timeout;
  const document = {
    documentElement: { dataset: {} },
    readyState: 'loading',
    visibilityState: 'visible',
    getElementById: () => notice,
    addEventListener() {}
  };
  const window = {
    PortfolioApp: { hasPendingEdits: () => editing },
    addEventListener(type, fn) { events[type] = fn; }
  };
  const navigator = { onLine: true, serviceWorker: { controller: {}, async register() {} } };
  vm.runInNewContext(fs.readFileSync('web/static/pwa.js', 'utf8'), {
    document, window, navigator, fetch, AbortController,
    location: { replace(url) { destinations.push(url); } },
    setTimeout(fn) { timeout = fn; }, clearTimeout() {}, setInterval() {}
  });
  return { events, destinations, notice, expire() { timeout(); } };
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
