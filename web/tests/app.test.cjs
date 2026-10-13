const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function loadApp(elements = {}, setTimer = callback => callback(), generation = '') {
  const source = fs.readFileSync('web/static/app.js', 'utf8').replace(
    '    init,\n    showToast,',
    '    init,\n    safeStorage,\n    saveDisplayCurrency,\n    initSources,\n    makeEditable,\n    initModalHandling,\n    initHoldingMutationTracking,\n    initDisclosureKeyboard,\n    initHistory,\n    initPullToRefresh,\n    updateChartColors,\n    getChartOptions,\n    snapshotChartValue,\n    sortTableRows,\n    showToast,');
  const pending = [];
  const listeners = {};
  const document = {
    body: { style: {}, classList: { contains: () => false } },
    readyState: 'loading', addEventListener(type, callback) { (listeners[type] ||= []).push(callback); },
    querySelector: () => null,
    getElementById: id => elements[id] || null,
    createTextNode: text => ({ textContent: text }),
    createElement: () => element(),
    querySelectorAll: selector => selector === '.snapshot-delete' ? (elements['snapshots-tbody']?.children || []).flatMap(row => row.cells?.[5]?.children || []) : [],
    documentElement: { getAttribute: () => 'light', dataset: { portfolioGeneration: generation } }
  };
  const context = { document, setTimeout: setTimer, window: { crypto: require('node:crypto').webcrypto, scrollY: 0, location: { href: 'https://portfolio.test/holdings?basket=one', assign() {}, reload() {} } }, URL, URLSearchParams,
    getComputedStyle: node => ({ overflowY: node.overflowY || 'visible', getPropertyValue: () => '#123456' }),
    Option: class { constructor(text, value) { this.text = text; this.value = value; } },
    FormData: class { constructor(form) { return form.formData; } },
    Chart: class { constructor(canvas, config) { this.config = config; this.options = config.options; this.data = config.data; } destroy() {} update() {} },
    fetch: (url, options) => new Promise((resolve, reject) => pending.push({ url, options, resolve, reject })) };
  vm.runInNewContext(source, context);
  return { app: context.window.PortfolioApp, pending, document, window: context.window,
    dispatch(type, event = {}) { for (const listener of listeners[type] || []) listener(event); } };
}

test('failed snapshots produce gaps while genuine zero values remain zero', () => {
  const { app } = loadApp();
  assert.equal(app.snapshotChartValue({ value_try: 0, failed_count: 0 }, 'TRY'), 0);
  assert.equal(app.snapshotChartValue({ value_try: 0, failed_count: 1 }, 'TRY'), null);
  assert.equal(app.snapshotChartValue({ value_usd: 5, failed_count: 1 }, 'USD'), null);
  assert.equal(app.snapshotChartValue({ value_usd: 5, stale_count: 1 }, 'USD'), 5);
  assert.equal(app.snapshotChartValue({ value_usd: 5, valuation_complete: false }, 'USD'), null);
  assert.equal(app.snapshotChartValue({ value_usd: 0, valuation_available: false }, 'USD'), null);
  assert.equal(app.snapshotChartValue({ value_usd: 0, valuation_available: true }, 'USD'), 0);
});

test('unavailable numeric cells sort after valid zero in both directions', () => {
  const { app } = loadApp();
  const rows = ['', '0', '12'].map(raw => ({ cells: [{ dataset: { sortValue: raw } }] }));
  const body = { rows, append(...items) { this.rows = items; } };
  rows.forEach(row => { row.parentNode = body; });
  const table = { tBodies: [body] };
  const header = { dataset: { sort: 'current_price' }, cellIndex: 0 };
  app.sortTableRows(table, header, 'asc');
  assert.deepEqual(body.rows.map(row => row.cells[0].dataset.sortValue), ['0', '12', '']);
  app.sortTableRows(table, header, 'desc');
  assert.deepEqual(body.rows.map(row => row.cells[0].dataset.sortValue), ['12', '0', '']);
  app.sortTableRows(table, header, 'none');
  assert.deepEqual(body.rows.map(row => row.cells[0].dataset.sortValue), ['', '0', '12']);
});

test('chart errors have retry controls and ignore obsolete responses', async () => {
  for (const [id, method, selections] of [
    ['portfolio-chart', 'loadPortfolioChart', ['1y', '1m']],
    ['allocation-chart', 'loadAllocationChart', ['type', 'currency']]
  ]) {
    const status = { replaceChildren(...children) { this.children = children; }, appendChild(child) { this.children.push(child); } };
    const { app, pending } = loadApp({ [id]: {}, [`${id}-status`]: status });
    app[method](selections[0]);
    app[method](selections[1]);
    pending[0].reject(new Error('private upstream detail'));
    await new Promise(resolve => setImmediate(resolve));
    assert.match(status.children[0].textContent, /Loading/);
    pending[1].reject(new Error('private upstream detail'));
    await new Promise(resolve => setImmediate(resolve));
    assert.match(status.children[0].textContent, /unavailable/);
    assert.doesNotMatch(status.children[0].textContent, /private/);
    assert.equal(status.children[1].textContent, 'Retry');
    status.children[1].click();
    assert.equal(pending.length, 3);
  }
});


test('rendered portfolio chart does not connect failed snapshot gaps', async () => {
  const status = { replaceChildren(...children) { this.children = children; } };
  const { app, pending } = loadApp({ 'portfolio-chart': {}, 'portfolio-chart-status': status });
  app.loadPortfolioChart('1m');
  pending[0].resolve({ ok: true, json: async () => [
    { timestamp: '2026-10-01', value_try: 100, failed_count: 0 },
    { timestamp: '2026-10-02', value_try: 5, failed_count: 1 },
    { timestamp: '2026-10-03', value_try: 110, failed_count: 0 }
  ] });
  await new Promise(resolve => setImmediate(resolve));
  const dataset = app.state().charts.portfolio.config.data.datasets[0];
  assert.deepEqual(Array.from(dataset.data), [100, null, 110]);
  assert.equal(dataset.spanGaps, false);
  assert.match(status.children[0].textContent, /may be incomplete/);
});

const flush = () => new Promise(resolve => setImmediate(resolve));
function element(value = '') {
  return { value, children: [], attrs: {}, dataset: {}, disabled: false, isConnected: true,
    addEventListener(type, callback) { this[type] = callback; },
    replaceChildren(...children) { this.children = children; },
    appendChild(child) { this.children.push(child); },
    setAttribute(key, value) { this.attrs[key] = value; },
    focus() { this.focused = true; },
    showModal() { this.open = true; },
    dismiss(value) { this.returnValue = value; this.open = false; this.close(); },
    insertRow() { const row = { dataset: {}, cells: [], remove: () => { row.cells.forEach(cell => cell.children.forEach(child => { child.isConnected = false; })); this.children = this.children.filter(child => child !== row); }, insertCell() { const cell = element(); this.cells.push(cell); return cell; } }; this.children.push(row); return row; },
    add(option) { this.children.push(option); }
  };
}
function historyApp() {
  const elements = Object.fromEntries(['date-range', 'filter-currency', 'holding-select', 'holding-basket', 'holding-location', 'reset-filters', 'first-page', 'prev-page', 'next-page', 'snapshot-value-heading', 'snapshots-tbody', 'snapshots-status', 'page-info', 'clear-history', 'history-delete-status', 'confirm-action', 'confirm-action-title', 'confirm-action-message', 'confirm-action-submit', 'portfolio-chart', 'portfolio-chart-status', 'holding-chart', 'holding-chart-status'].map(id => [id, element()]));
  elements['date-range'].value = '365';
  elements['filter-currency'].value = 'USD';
  const harness = loadApp(elements);
  harness.app.initHistory();
  return { ...harness, elements, requests: () => harness.pending.filter(p => p.url.includes('/snapshots?')) };
}
const snapshots = count => Array.from({ length: count }, (_, index) => ({ id: index + 1, timestamp: '2026-10-01', total_value_usd: 10, total_value_try: 300 }));
const respond = (request, count) => request.resolve({ ok: true, json: async () => snapshots(count) });

test('theme colors preserve USD portfolio and holding callbacks while default currency is TRY', () => {
  const { app, document } = loadApp();
  for (const name of ['portfolio', 'holding']) {
    app.state().charts[name] = { options: app.getChartOptions('USD'), update() {} };
  }
  const charts = [app.state().charts.portfolio, app.state().charts.holding];
  const callbacks = charts.map(chart => chart.options.plugins.tooltip.callbacks.label);
  const tickCallbacks = charts.map(chart => chart.options.scales.y.ticks.callback);
  document.documentElement.getAttribute = () => 'dark';
  app.updateChartColors();
  charts.forEach((chart, i) => {
    assert.equal(chart.options.plugins.tooltip.callbacks.label, callbacks[i]);
    assert.equal(chart.options.scales.y.ticks.callback, tickCallbacks[i]);
    assert.match(callbacks[i]({ dataset: { label: 'USD' }, raw: 10 }), /\$/);
    assert.equal(chart.options.plugins.tooltip.backgroundColor, '#1e293b');
  });
});

test('initial snapshot failure retries offset zero and never enables navigation', async () => {
  const h = historyApp();
  h.requests()[0].reject(new Error('unavailable'));
  await flush();
  for (const id of ['first-page', 'prev-page', 'next-page']) assert.equal(h.elements[id].disabled, true);
  assert.equal(h.elements['snapshots-tbody'].attrs['aria-busy'], 'false');
  assert.match(h.elements['snapshots-status'].children[0].textContent, /Unable/);
  h.elements['snapshots-status'].children[1].click();
  assert.match(h.requests()[1].url, /offset=0&/);
  respond(h.requests()[1], 51);
  await flush();
  assert.equal(h.elements['page-info'].textContent, 'Page 1');
  assert.equal(h.elements['next-page'].disabled, false);
  assert.equal(h.elements['snapshots-status'].children.length, 0);
});

test('failed snapshot pagination retains valid page and retries attempted offset', async () => {
  const h = historyApp();
  respond(h.requests()[0], 51); await flush();
  h.elements['next-page'].click();
  h.requests()[1].reject(new Error('unavailable')); await flush();
  assert.equal(h.elements['page-info'].textContent, 'Page 1');
  assert.equal(h.elements['snapshots-tbody'].children.length, 50);
  assert.equal(h.elements['next-page'].disabled, false);
  assert.equal(h.elements['prev-page'].disabled, true);
  h.elements['snapshots-status'].children[1].click();
  assert.match(h.requests()[2].url, /offset=50&/);
  respond(h.requests()[2], 2); await flush();
  assert.equal(h.elements['page-info'].textContent, 'Page 2');
  assert.equal(h.elements['prev-page'].disabled, false);
  assert.equal(h.elements['next-page'].disabled, true);
  h.elements['prev-page'].click();
  h.requests()[3].reject(new Error('unavailable')); await flush();
  assert.equal(h.elements['next-page'].disabled, true);
  assert.equal(h.elements['prev-page'].disabled, false);
  h.elements['snapshots-status'].children[1].click();
  assert.match(h.requests()[4].url, /offset=0&/);
});

test('changed snapshot filters clear old navigation and obsolete results cannot alter state', async () => {
  for (const obsoleteFails of [false, true]) {
    const h = historyApp();
    respond(h.requests()[0], 51); await flush();
    h.elements['next-page'].click();
    h.elements['date-range'].value = '30';
    h.elements['date-range'].change();
    assert.equal(h.elements['snapshots-tbody'].children.length, 0);
    if (obsoleteFails) h.requests()[1].reject(new Error('old failure'));
    else respond(h.requests()[1], 51);
    await flush();
    assert.equal(h.elements['next-page'].disabled, true);
    assert.equal(h.elements['snapshots-tbody'].attrs['aria-busy'], 'true');
    assert.match(h.elements['snapshots-status'].children[0].textContent, /Loading/);
    h.requests()[2].reject(new Error('new failure')); await flush();
    assert.equal(h.elements['next-page'].disabled, true);
    h.elements['snapshots-status'].children[1].click();
    assert.match(h.requests()[3].url, /offset=0&days=30/);
  }
});

function pullHarness() {
  const h = loadApp();
  h.app.initPullToRefresh();
  h.target = { closest: () => null, parentElement: h.document.body };
  h.gesture = (target = h.target) => {
    h.dispatch('touchstart', { target, touches: [{ clientY: 10 }] });
    h.dispatch('touchmove', { target, touches: [{ clientY: 90 }], preventDefault() {} });
    h.dispatch('touchend');
  };
  return h;
}

test('pull refresh ignores modals, dirty forms, controls and nested scrollers', () => {
  for (const reason of ['modal', 'dirty', 'control', 'nested', 'inline', 'preexisting']) {
    const h = pullHarness();
    if (reason === 'modal') h.document.body.classList.contains = () => true;
    if (reason === 'inline') h.document.querySelector = () => ({});
    if (reason === 'dirty' || reason === 'preexisting') {
      const form = { elements: reason === 'preexisting' ? [{ tagName: 'INPUT', value: 'edited', defaultValue: '' }] : [] };
      h.document.querySelectorAll = () => [form];
      if (reason === 'dirty') h.dispatch('input', { target: { form } });
    }
    if (reason === 'control') h.target.closest = () => ({});
    if (reason === 'nested') h.target.parentElement = { scrollHeight: 500, clientHeight: 100, overflowY: 'auto', parentElement: h.document.body };
    h.gesture();
    assert.equal(h.pending.length, 0, reason);
  }
});

test('pull refresh prevents overlapping requests and does not reload edits begun in flight', async () => {
  const h = pullHarness();
  let reloads = 0;
  h.window.location.reload = () => reloads++;
  h.gesture(); h.gesture();
  assert.equal(h.pending.length, 1);
  assert.equal(h.pending[0].url, '/api/sources/refresh');
  h.document.body.classList.contains = () => true;
  h.pending[0].resolve({ ok: true }); await flush();
  assert.equal(reloads, 0);
  h.document.body.classList.contains = () => false;
  h.gesture();
  h.pending[1].resolve({ ok: true }); await flush();
  assert.equal(reloads, 1);
});

test('cancelled, multi-touch, or newly blocked pull gestures do not refresh', () => {
  for (const cancel of ['touchcancel', 'multitouch', 'modal']) {
    const h = pullHarness();
    h.dispatch('touchstart', { target: h.target, touches: [{ clientY: 10 }] });
    h.dispatch('touchmove', { target: h.target, touches: [{ clientY: 90 }], preventDefault() {} });
    if (cancel === 'touchcancel') h.dispatch('touchcancel');
    if (cancel === 'multitouch') h.dispatch('touchmove', { target: h.target, touches: [{ clientY: 90 }, { clientY: 95 }] });
    if (cancel === 'modal') h.document.body.classList.contains = () => true;
    h.dispatch('touchend');
    assert.equal(h.pending.length, 0);
    assert.ok(['', 'translateY(0)'].includes(h.document.body.style.transform));
  }
});

test('obsolete snapshot failure cannot overwrite a newer successful page', async () => {
  const h = historyApp();
  h.elements['date-range'].value = '30';
  h.elements['date-range'].change();
  respond(h.requests()[1], 2); await flush();
  h.requests()[0].reject(new Error('obsolete failure')); await flush();
  assert.equal(h.elements['snapshots-status'].children.length, 0);
  assert.equal(h.elements['snapshots-tbody'].children.length, 2);
  assert.equal(h.elements['page-info'].textContent, 'Page 1');
  assert.equal(h.elements['snapshots-tbody'].attrs['aria-busy'], 'false');
  assert.equal(h.elements['next-page'].disabled, true);
  assert.equal(h.elements['prev-page'].disabled, true);
});

const deleteRequests = h => h.pending.filter(request => request.options?.method === 'DELETE');
const rowDelete = (h, index = 0) => h.elements['snapshots-tbody'].children[index].cells[5].children[0];
async function confirmDelete(h, all = false) {
  if (all) h.elements['clear-history'].click();
  else rowDelete(h).click();
  h.elements['confirm-action'].dismiss('confirm');
  await flush();
}

test('snapshot deletion and clear history cancel without requests, and confirmations cannot overlap', async () => {
  for (const all of [false, true]) {
    const h = historyApp(); respond(h.requests()[0], 2); await flush();
    const first = rowDelete(h);
    assert.equal(first.attrs['aria-label'], 'Delete snapshot from 2026-10-01T00:00:00.000Z UTC');
    if (all) h.elements['clear-history'].click(); else first.click();
    const title = h.elements['confirm-action-title'].textContent;
    first.click(); h.elements['clear-history'].click();
    assert.equal(h.elements['confirm-action-title'].textContent, title);
    assert.equal(first.disabled, true);
    const message = h.elements['confirm-action-message'].textContent;
    assert.match(message, /Permanently delete/);
    assert.match(message, /Holdings and settings will stay unchanged/);
    if (all) {
      assert.match(message, /ALL snapshots across every date range/);
      assert.match(message, /outside the selected range/);
      assert.match(message, /Hourly snapshots will resume/);
    }
    h.elements['confirm-action'].dismiss('cancel'); await flush();
    assert.equal(deleteRequests(h).length, 0);
    assert.equal(h.elements['snapshots-tbody'].children.length, 2);
    assert.equal(first.disabled, false);
    assert.equal((all ? h.elements['clear-history'] : first).focused, true);
    assert.equal(h.elements['clear-history'].disabled, false);
  }
});

test('delete failures retain rows and filters, report errors, and allow retry without duplicate submissions', async () => {
  for (const all of [false, true]) {
    const h = historyApp(); respond(h.requests()[0], 2); await flush();
    await confirmDelete(h, all);
    rowDelete(h).click(); h.elements['clear-history'].click();
    assert.equal(deleteRequests(h).length, 1);
    deleteRequests(h)[0].resolve({ ok: false, status: 500 }); await flush();
    respond(h.requests()[1], 2); await flush();
    assert.equal(h.elements['snapshots-tbody'].children.length, 2);
    assert.match(h.elements['history-delete-status'].textContent, /Unable to confirm deletion/);
    assert.equal(h.elements['date-range'].value, '365');
    assert.equal(h.elements['filter-currency'].value, 'USD');
    assert.equal(rowDelete(h).disabled, false);
    await confirmDelete(h, all);
    assert.equal(deleteRequests(h).length, 2);
    deleteRequests(h)[1].reject(new Error('offline')); await flush();
    respond(h.requests()[2], 2); await flush();
    assert.equal(h.elements['clear-history'].disabled, false);
    assert.equal(h.elements['snapshots-tbody'].children.length, 2);
  }
});

test('individual delete and clear history reload both charts and snapshots while preserving filters', async () => {
  for (const all of [false, true]) {
    const h = historyApp(); respond(h.requests()[0], 2); await flush();
    h.elements['holding-select'].value = '9';
    h.elements['holding-basket'].value = 'Savings';
    await confirmDelete(h, all);
    const mutation = deleteRequests(h)[0];
    assert.equal(mutation.url, all ? '/api/history/snapshots' : '/api/history/snapshots/1');
    assert.equal(mutation.options.headers?.['X-Clear-History-Confirm'], all ? 'clear-history' : undefined);
    mutation.resolve({ ok: true, status: 204 }); await flush();
    assert.equal(h.elements['snapshots-tbody'].children.length, all ? 0 : 1);
    assert.ok(h.pending.some(request => request.url === '/api/history/chart?days=365'));
    assert.ok(h.pending.some(request => request.url === '/api/history/holding/9?days=365'));
    assert.match(h.requests()[1].url, /offset=0&days=365/);
    respond(h.requests()[1], all ? 0 : 1); await flush();
    assert.equal(h.elements['holding-select'].value, '9');
    assert.equal(h.elements['holding-basket'].value, 'Savings');
    assert.equal(h.elements['filter-currency'].value, 'USD');
    assert.equal(h.elements['clear-history'].disabled, false);
    assert.equal(h.elements['clear-history'].focused, true);
    assert.match(h.elements['history-delete-status'].textContent, /unchanged/);
    if (all) assert.match(h.elements['snapshots-tbody'].children[0].cells[0].textContent, /No snapshots/);
  }
});

test('deleting final snapshot on final page backtracks to a valid page', async () => {
  const h = historyApp(); respond(h.requests()[0], 51); await flush();
  h.elements['next-page'].click(); respond(h.requests()[1], 1); await flush();
  await confirmDelete(h);
  deleteRequests(h)[0].resolve({ ok: true }); await flush();
  assert.match(h.requests()[2].url, /offset=50&/);
  respond(h.requests()[2], 0); await flush();
  assert.match(h.requests()[3].url, /offset=0&/);
  respond(h.requests()[3], 50); await flush();
  assert.equal(h.elements['page-info'].textContent, 'Page 1');
  assert.equal(h.elements['prev-page'].disabled, true);
  assert.equal(h.elements['next-page'].disabled, true);
  assert.equal(h.elements['snapshots-tbody'].attrs['aria-busy'], 'false');
  assert.equal(h.elements['clear-history'].disabled, false);
});

test('reads started before deletion cannot restore snapshot rows or either chart', async () => {
  const h = historyApp(); respond(h.requests()[0], 51); await flush();
  h.elements['holding-select'].value = '9';
  h.elements['holding-select'].change();
  h.app.loadPortfolioChart();
  const oldHolding = h.pending.find(request => request.url.includes('/history/holding/'));
  const oldPortfolio = h.pending.find(request => request.url.includes('/history/chart?'));
  h.elements['next-page'].click();
  const oldPage = h.requests()[1];
  await confirmDelete(h, true);
  deleteRequests(h)[0].resolve({ ok: true }); await flush();
  respond(h.requests()[2], 0); await flush();
  respond(oldPage, 51);
  oldHolding.resolve({ ok: true, json: async () => [{ timestamp: '2026-10-01', value_usd: 999 }] });
  oldPortfolio.resolve({ ok: true, json: async () => [{ timestamp: '2026-10-01', value_usd: 999 }] });
  await flush();
  assert.match(h.elements['snapshots-tbody'].children[0].cells[0].textContent, /No snapshots/);
  assert.equal(h.app.state().charts.portfolio, null);
  assert.equal(h.app.state().charts.holding, null);
  assert.equal(h.elements['page-info'].textContent, 'Page 1');
});


test('failed deletion reconciles interrupted reads and blocks chart retries during mutation', async () => {
  const h = historyApp(); respond(h.requests()[0], 51); await flush();
  h.elements['holding-select'].value = '9';
  h.elements['holding-select'].change(); h.app.loadPortfolioChart();
  h.elements['next-page'].click();
  await confirmDelete(h);
  const before = h.pending.length;
  h.app.loadPortfolioChart(); h.elements['holding-select'].change();
  assert.equal(h.pending.length, before);
  deleteRequests(h)[0].reject(new Error('lost response')); await flush();
  assert.equal(h.pending.filter(request => request.url.includes('/history/chart?')).length, 2);
  assert.equal(h.pending.filter(request => request.url.includes('/history/holding/')).length, 2);
  assert.equal(h.elements['snapshots-tbody'].children.length, 50);
  respond(h.requests()[2], 0); await flush();
  respond(h.requests()[1], 51); await flush();
  assert.match(h.elements['snapshots-tbody'].children[0].cells[0].textContent, /No snapshots/);
  assert.equal(h.elements['snapshots-tbody'].attrs['aria-busy'], 'false');
  assert.equal(h.elements['clear-history'].disabled, false);
  assert.match(h.elements['history-delete-status'].textContent, /Unable to confirm deletion/);
});


test('delete removes matching row even when a pending read replaced it during confirmation', async () => {
  const h = historyApp(); respond(h.requests()[0], 2); await flush();
  h.elements['first-page'].click();
  rowDelete(h).click();
  respond(h.requests()[1], 2); await flush();
  h.elements['confirm-action'].dismiss('confirm'); await flush();
  deleteRequests(h)[0].resolve({ ok: true }); await flush();
  h.requests()[2].reject(new Error('reload failed')); await flush();
  assert.equal(h.elements['snapshots-tbody'].children.length, 1);
  assert.equal(h.elements['snapshots-tbody'].children[0].dataset.snapshotId, '2');
  assert.match(h.elements['snapshots-status'].children[0].textContent, /Unable to load/);
});

test('restricted storage falls back locally and the early theme script still applies server preference', () => {
  const h = loadApp();
  Object.defineProperty(h.window, 'localStorage', { get() { throw new Error('SecurityError'); } });
  const storage = h.app.safeStorage('localStorage');
  assert.equal(storage.getItem('theme'), null);
  storage.setItem('theme', 'dark');
  assert.equal(storage.getItem('theme'), 'dark');
  storage.removeItem('theme');
  assert.equal(storage.getItem('theme'), null);
  const root = { dataset: { themePreference: 'dark' }, setAttribute(key, value) { this[key] = value; } };
  const early = fs.readFileSync('web/templates/base.html', 'utf8').match(/<!-- Theme detection -->\s*<script>([\s\S]*?)<\/script>/)[1];
  const context = { document: { documentElement: root } };
  Object.defineProperty(context, 'localStorage', { get() { throw new Error('SecurityError'); } });
  vm.runInNewContext(early, context);
  assert.equal(root['data-theme'], 'dark');
});

test('currency updates share one lock across controls and failures recover without navigation', async () => {
  const select = element('USD'), first = element(), second = element();
  const h = loadApp({ 'display-currency': select });
  h.document.querySelectorAll = () => [select, first, second];
  let destination;
  h.window.location.assign = value => { destination = value; };
  h.app.saveDisplayCurrency('USD');
  h.app.saveDisplayCurrency('TRY');
  assert.equal(h.pending.length, 1);
  assert.ok([select, first, second].every(control => control.disabled));
  h.pending[0].reject(new Error('offline')); await flush();
  assert.equal(destination, undefined);
  assert.equal(select.value, 'TRY');
  assert.ok([select, first, second].every(control => !control.disabled));
  h.app.saveDisplayCurrency('USD');
  h.pending[1].resolve({ ok: true }); await flush();
  assert.equal(destination, 'https://portfolio.test/holdings?basket=one&currency=USD');
  h.app.saveDisplayCurrency('TRY');
  assert.equal(h.pending.length, 2);
});

test('source operations lock the entire row and restore previous disabled state after failure', async () => {
  const page = element(), row = element(), button = element(), input = element(), select = element(), blocked = element();
  blocked.disabled = true;
  page.querySelectorAll = () => [input, select];
  row.querySelectorAll = () => [button, input, select, blocked];
  row.querySelector = selector => selector === '.provider-select' ? { value: 'Yahoo Finance' } :
    selector === '.source-test-result' ? null : { value: 'AAPL' };
  button.closest = () => row;
  button.hasAttribute = () => true;
  row.dataset.holdingId = '1';
  const h = loadApp();
  h.document.querySelector = () => page;
  h.app.initSources();
  const event = { target: { closest: () => button } };
  page.click(event); page.click(event);
  assert.equal(h.pending.length, 1);
  assert.ok([button, input, select, blocked].every(control => control.disabled));
  assert.equal(row.attrs['aria-busy'], 'true');
  h.pending[0].reject(new Error('offline')); await flush();
  assert.ok([button, input, select].every(control => !control.disabled));
  assert.equal(blocked.disabled, true);
  assert.equal(row.attrs['aria-busy'], 'false');
  page.click(event);
  assert.equal(h.pending.length, 2);
});

function inlineHarness() {
  const h = loadApp();
  const td = element();
  td.dataset = { field: 'quantity', sortValue: '10' };
  td.textContent = '10';
  td.closest = () => ({ dataset: { id: '1' } });
  td.removeAttribute = key => { delete td.attrs[key]; };
  let input;
  h.document.createElement = () => {
    input = element(); input.style = {}; input.select = () => {};
    input.reportValidity = () => true;
    input.focus = () => { h.document.activeElement = input; };
    return input;
  };
  h.app.makeEditable(td);
  return { ...h, td, input };
}

test('inline save is read-only and cannot appear cancelled while its request is pending', async () => {
  const h = inlineHarness();
  assert.equal(h.input.attrs['aria-label'], 'Edit quantity');
  h.input.value = '20';
  h.input.keydown({ key: 'Enter', preventDefault() {} });
  assert.equal(h.input.readOnly, true);
  assert.equal(h.td.attrs['aria-busy'], 'true');
  assert.equal(h.input.attrs['aria-label'], 'Saving quantity…');
  h.input.keydown({ key: 'Escape', preventDefault() {} });
  h.input.blur();
  assert.equal(h.pending.length, 1);
  assert.equal(h.td.attrs['aria-busy'], 'true');
  h.pending[0].reject(new Error('offline')); await flush();
  assert.equal(h.td.textContent, '10');
  assert.equal(h.td.focused, true);
  assert.equal(h.td.attrs['aria-busy'], undefined);
});

test('inline cancellation and unchanged keyboard save return focus to the cell', () => {
  for (const key of ['Escape', 'Enter']) {
    const h = inlineHarness();
    h.input.keydown({ key, preventDefault() {} });
    assert.equal(h.pending.length, 0);
    assert.equal(h.td.focused, true);
  }
});

test('Escape closes every disclosure menu and returns focus only to the active trigger', () => {
  for (const kind of ['currency', 'column', 'action']) {
    const menu = element(), toggle = element(), child = {};
    toggle.getAttribute = key => key === 'aria-controls' ? `${kind}-menu` : toggle.attrs[key];
    toggle.classList = { contains: () => kind === 'action' };
    toggle.closest = () => ({ querySelector: () => menu });
    toggle.attrs['aria-expanded'] = 'true';
    menu.contains = target => target === child;
    const h = loadApp({ [`${kind}-menu`]: menu });
    h.document.querySelectorAll = () => [toggle];
    h.app.initDisclosureKeyboard();
    let prevented = false;
    h.dispatch('keydown', { key: 'Escape', target: child, preventDefault() { prevented = true; } });
    assert.equal(toggle.attrs['aria-expanded'], 'false');
    assert.equal(menu.attrs['aria-hidden'], 'true');
    assert.equal(toggle.focused, true);
    assert.equal(prevented, true);
  }
});

test('allocation charts update all theme colors without losing percentage callbacks', async () => {
  const h = loadApp({ 'allocation-chart': {}, 'allocation-chart-status': element() });
  h.app.loadAllocationChart('type');
  h.pending[0].resolve({ ok: true, json: async () => ({ by_type: { Stock: 5 } }) });
  await flush();
  const chart = h.app.state().charts.allocation;
  const callback = chart.options.plugins.tooltip.callbacks.label;
  h.document.documentElement.getAttribute = () => 'dark';
  h.app.updateChartColors();
  assert.equal(chart.data.datasets[0].borderColor, '#123456');
  assert.equal(chart.options.plugins.legend.labels.color, '#94a3b8');
  assert.equal(chart.options.plugins.tooltip.backgroundColor, '#1e293b');
  assert.equal(chart.options.plugins.tooltip.callbacks.label, callback);
  assert.match(callback({ raw: 5, label: 'Stock' }), /100.0%/);
});

test('the full application initializes even when both browser storage getters throw', () => {
  const h = loadApp();
  for (const name of ['localStorage', 'sessionStorage']) {
    Object.defineProperty(h.window, name, { get() { throw new Error('SecurityError'); } });
  }
  h.document.documentElement.dataset = { currency: 'USD', themePreference: 'light' };
  h.document.documentElement.setAttribute = () => {};
  const bodyEvents = [];
  h.document.body.addEventListener = name => bodyEvents.push(name);
  h.window.addEventListener = () => {};
  assert.doesNotThrow(() => h.app.init());
  assert.equal(h.app.state().displayCurrency, 'USD');
  assert.ok(bodyEvents.includes('htmx:beforeRequest'));
});

test('storage adapter does not retry or duplicate persistence writes', () => {
  const h = loadApp();
  let writes = 0;
  h.window.localStorage = {
    getItem: () => 'light',
    setItem() { writes++; throw new Error('QuotaExceededError'); },
    removeItem() { writes++; throw new Error('SecurityError'); }
  };
  const storage = h.app.safeStorage('localStorage');
  assert.equal(storage.getItem('theme'), 'light');
  storage.setItem('theme', 'dark');
  assert.equal(storage.getItem('theme'), 'dark');
  storage.removeItem('theme');
  assert.equal(storage.getItem('theme'), null);
  assert.equal(writes, 2);
});

// Keep reload timers explicit so request completion and the next interaction can
// be interleaved deterministically rather than relying on wall-clock delays.
function inlineRaceHarness() {
  const timers = [];
  const elements = {};
  const h = loadApp(elements, callback => timers.push(callback));
  const cells = [];
  h.forms = [];
  h.modal = false;
  h.reloads = 0;
  h.window.location.reload = () => { h.reloads++; };
  h.runTimers = () => {
    for (const callback of timers.splice(0)) callback();
  };
  h.document.body.classList.contains = () => h.modal;
  h.document.querySelector = selector => selector.includes('.modal-overlay') ?
    (h.modal ? {} : cells.flatMap(td => td.children).find(input => input.className === 'inline-edit-input') || null) : null;
  h.document.querySelectorAll = selector => selector === 'form' ? h.forms : [];
  h.document.createElement = tag => {
    const node = element();
    node.tagName = tag.toUpperCase();
    node.style = {};
    node.select = () => {};
    node.reportValidity = () => true;
    node.removeAttribute = key => { delete node.attrs[key]; };
    node.focus = () => { h.document.activeElement = node; node.focused = true; };
    node.matches = selector => selector.split(',').some(part => part.trim().toUpperCase() === node.tagName);
    node.querySelector = () => element();
    let text = '';
    for (const property of ['textContent', 'innerHTML']) Object.defineProperty(node, property, {
      get: () => text,
      set(value) { text = value; node.children = []; }
    });
    return node;
  };
  h.notice = h.document.createElement('div');
  elements['toast-container'] = h.notice;
  h.edit = (field = 'quantity', value = '10', id = String(cells.length + 1)) => {
    const td = h.document.createElement('td');
    td.dataset = { field, sortValue: value, editValue: value };
    td.textContent = value;
    td.closest = () => ({ dataset: { id } });
    cells.push(td);
    h.app.makeEditable(td);
    return { td, input: td.children[0] };
  };
  h.save = (edit, value) => {
    edit.input.value = value;
    edit.input.keydown({ key: 'Enter', preventDefault() {} });
  };
  return h;
}

test('inline happy path displays only the accepted value and schedules one safe reload', async () => {
  const h = inlineRaceHarness();
  const edit = h.edit();
  h.save(edit, '20');
  assert.equal(edit.td.dataset.sortValue, '10');
  assert.equal(edit.td.attrs['aria-busy'], 'true');
  h.pending[0].resolve({ ok: true }); await flush();
  assert.equal(edit.td.dataset.sortValue, '20');
  assert.equal(edit.td.textContent, Number(20).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 8 }));
  assert.equal(edit.td.attrs['aria-busy'], undefined);
  assert.equal(h.document.activeElement, edit.td);
  assert.equal(h.reloads, 0);
  h.runTimers(); h.runTimers();
  assert.equal(h.reloads, 1);
});

test('an older inline save cannot reload or steal focus from a newer unsaved editor', async () => {
  for (const startBeforeResponse of [true, false]) {
    const h = inlineRaceHarness();
    const first = h.edit();
    h.save(first, '20');
    let next;
    if (startBeforeResponse) next = h.edit('notes', 'Original note');
    h.pending[0].resolve({ ok: true }); await flush();
    if (!startBeforeResponse) next = h.edit('notes', 'Original note');
    next.input.value = 'Important unsaved note';
    h.runTimers();
    assert.equal(h.reloads, 0);
    assert.equal(h.document.activeElement, next.input);
    assert.equal(next.input.value, 'Important unsaved note');
    assert.equal(next.td.children[0], next.input);
    assert.equal(h.pending.length, 1);
    assert.match(h.notice.children[0].textContent, /refresh to update totals/);
    assert.equal(h.notice.children[0].attrs.role, 'status');
  }
});

test('inline reload waits for every pending save and coalesces either response order', async () => {
  for (const order of [[0, 1], [1, 0]]) {
    const h = inlineRaceHarness();
    const first = h.edit(); h.save(first, '20');
    const second = h.edit('notes', 'Before'); h.save(second, 'After');
    h.pending[order[0]].resolve({ ok: true }); await flush(); h.runTimers();
    assert.equal(h.reloads, 0);
    assert.equal([first, second][order[1]].td.attrs['aria-busy'], 'true');
    h.pending[order[1]].resolve({ ok: true }); await flush(); h.runTimers(); h.runTimers();
    assert.equal(h.reloads, 1);
    assert.equal(first.td.dataset.sortValue, '20');
    assert.equal(second.td.dataset.editValue, 'After');
    assert.equal(first.td.attrs['aria-busy'], undefined);
    assert.equal(second.td.attrs['aria-busy'], undefined);
  }
});

test('inline success never reloads over an active modal, focused form, or dirty form', async () => {
  const cases = [
    h => { h.modal = true; },
    h => { h.document.activeElement = h.document.createElement('input'); },
    h => { h.forms = [{ elements: [{ tagName: 'TEXTAREA', value: 'draft', defaultValue: '' }] }]; },
    h => { h.forms = [{ elements: [{ type: 'checkbox', checked: true, defaultChecked: false }] }]; },
    h => { h.forms = [{ elements: [{ type: 'file', files: [{}] }] }]; },
    h => { h.forms = [{ elements: [{ tagName: 'SELECT', selectedIndex: 1, options: [{ defaultSelected: false }, { defaultSelected: false }] }] }]; },
    h => { h.forms = [{ elements: [{ tagName: 'SELECT', multiple: true, options: [{ selected: true, defaultSelected: false }] }] }]; }
  ];
  for (const block of cases) {
    const h = inlineRaceHarness();
    const edit = h.edit(); h.save(edit, '20');
    block(h);
    h.pending[0].resolve({ ok: true }); await flush(); h.runTimers();
    assert.equal(h.reloads, 0);
    assert.equal(edit.td.dataset.sortValue, '20');
    assert.match(h.notice.children[0].textContent, /refresh/);
  }
});

test('unchanged forms and implicit select defaults allow a safe inline reload', async () => {
  const h = inlineRaceHarness();
  h.forms = [{ elements: [
    { tagName: 'INPUT', value: 'original', defaultValue: 'original' },
    { tagName: 'SELECT', selectedIndex: 0, options: [{ defaultSelected: false }, { defaultSelected: false }] },
    { tagName: 'SELECT', selectedIndex: 1, options: [{ defaultSelected: false }, { defaultSelected: true }] }
  ] }];
  const edit = h.edit(); h.save(edit, '20');
  h.pending[0].resolve({ ok: true }); await flush(); h.runTimers();
  assert.equal(h.reloads, 1);
});

test('a failed inline save preserves successful siblings and prevents a surprise reload', async () => {
  for (const failureFirst of [true, false]) {
    for (const networkFailure of [true, false]) {
      const h = inlineRaceHarness();
      const first = h.edit(); h.save(first, '20');
      const second = h.edit('notes', 'Before'); h.save(second, 'After');
      const fail = () => networkFailure ? h.pending[1].reject(new Error('offline')) : h.pending[1].resolve({ ok: false, status: 500 });
      if (failureFirst) fail(); else h.pending[0].resolve({ ok: true });
      await flush(); h.runTimers();
      assert.equal(h.reloads, 0);
      if (failureFirst) h.pending[0].resolve({ ok: true }); else fail();
      await flush(); h.runTimers();
      assert.equal(h.reloads, 0);
      assert.equal(first.td.dataset.sortValue, '20');
      assert.equal(second.td.dataset.editValue, 'Before');
      assert.equal(second.td.textContent, 'Before');
      assert.equal(second.td.attrs['aria-busy'], undefined);
      assert.ok(h.notice.children.some(toast => /Unable to save/.test(toast.innerHTML)));
      assert.ok(h.notice.children.some(toast => /refresh to update totals/.test(toast.textContent)));
    }
  }
});

test('Escape or an unchanged edit releases a deferred inline refresh without another request', async () => {
  for (const key of ['Escape', 'Enter']) {
    const h = inlineRaceHarness();
    const first = h.edit(); h.save(first, '20');
    const second = h.edit('notes', 'Original note');
    h.pending[0].resolve({ ok: true }); await flush(); h.runTimers();
    assert.equal(h.reloads, 0);
    if (key === 'Escape') second.input.value = 'Discard this draft';
    second.input.keydown({ key, preventDefault() {} });
    h.runTimers();
    assert.equal(h.reloads, 1);
    assert.equal(h.pending.length, 1);
    assert.equal(second.td.textContent, 'Original note');
  }
});

test('an invalid newer inline edit stays open and blocks an earlier successful save from reloading', async () => {
  const h = inlineRaceHarness();
  const first = h.edit(); h.save(first, '20');
  const second = h.edit(); second.input.reportValidity = () => false;
  h.save(second, '-1');
  h.pending[0].resolve({ ok: true }); await flush(); h.runTimers();
  assert.equal(h.reloads, 0);
  assert.equal(h.pending.length, 1);
  assert.equal(second.td.children[0], second.input);
  assert.equal(second.input.value, '-1');
});

test('a pending inline editor cannot be replaced by another editor for the same cell', async () => {
  const h = inlineRaceHarness();
  const edit = h.edit(); h.save(edit, '20');
  h.app.makeEditable(edit.td);
  assert.equal(edit.td.children[0], edit.input);
  assert.equal(h.pending.length, 1);
  h.pending[0].resolve({ ok: true }); await flush(); h.runTimers();
  assert.equal(edit.td.dataset.sortValue, '20');
  assert.equal(h.reloads, 1);
});

test('409 and 428 inline failures preserve drafts and require an explicit reload before retry', async () => {
  for (const status of [409, 428]) {
    const h = inlineRaceHarness();
    const accepted = h.edit(); h.save(accepted, '20');
    const conflicted = h.edit('notes', 'Original note'); h.save(conflicted, 'Keep this failed draft');
    const newest = h.edit('notes', 'Another note'); newest.input.value = 'Keep this unsaved draft too';
    h.pending[0].resolve({ ok: true });
    h.pending[1].resolve({ ok: false, status });
    await flush(); h.runTimers();
    assert.equal(h.reloads, 0);
    assert.equal(conflicted.td.children[0], conflicted.input);
    assert.equal(conflicted.input.value, 'Keep this failed draft');
    assert.equal(conflicted.input.readOnly, false);
    assert.match(conflicted.input.attrs['aria-label'], /reload required/);
    assert.equal(conflicted.td.attrs['aria-busy'], undefined);
    assert.equal(conflicted.td.dataset.editValue, 'Original note');
    assert.equal(h.document.activeElement, newest.input);
    assert.equal(newest.input.value, 'Keep this unsaved draft too');
    assert.ok(h.notice.children.some(toast => /reload before trying again/i.test(toast.innerHTML)));
    h.save(conflicted, 'Revised draft'); conflicted.input.blur();
    assert.equal(h.pending.length, 2, 'a stale page must not keep submitting the retained draft');
    assert.equal(conflicted.input.value, 'Revised draft');
    conflicted.input.keydown({ key: 'Escape', preventDefault() {} }); h.runTimers();
    assert.equal(conflicted.td.textContent, 'Original note');
    assert.equal(h.document.activeElement, newest.input);
    assert.equal(h.reloads, 0);
  }
});


test('mutation headers retain the initial page generation across newer reads and DOM changes', async () => {
  const h = loadApp({ 'portfolio-chart': {}, 'portfolio-chart-status': element() }, undefined, '7');
  h.document.documentElement.dataset.portfolioGeneration = '8';
  h.app.loadPortfolioChart('1y');
  h.pending[0].resolve({ ok: true, headers: { get: () => '8' }, json: async () => [] });
  await flush();
  h.app.saveDisplayCurrency('USD');
  assert.equal(h.pending[1].options.headers['X-Portfolio-Generation'], '7');
});

test('successful clear-history rebuilds the page with current filters and a fresh generation', async () => {
  const h = historyApp();
  h.window.location.href = 'https://portfolio.test/history';
  let destination;
  h.window.location.assign = value => { destination = value; };
  h.elements['date-range'].value = '30';
  respond(h.requests()[0], 2); await flush();
  await confirmDelete(h, true);
  deleteRequests(h)[0].resolve({ ok: true, status: 204 }); await flush();
  assert.equal(destination, 'https://portfolio.test/history?days=30&currency=USD');
});

test('history conflicts preserve the page and explain how to reload safely', async () => {
  for (const status of [409, 428]) {
    const h = historyApp();
    let navigated = false;
    h.window.location.assign = () => { navigated = true; };
    h.window.location.reload = () => { navigated = true; };
    respond(h.requests()[0], 2); await flush();
    await confirmDelete(h, false);
    deleteRequests(h)[0].resolve({ ok: false, status }); await flush();
    assert.match(h.elements['history-delete-status'].textContent, /Portfolio changed.*unsaved edits.*reload/);
    assert.equal(navigated, false);
  }
});

function holdingMutationHarness() {
  const h = inlineRaceHarness();
  const events = {};
  h.document.body.addEventListener = (type, callback) => { (events[type] ||= []).push(callback); };
  h.emit = (type, detail) => { for (const callback of events[type] || []) callback({ detail }); };
  h.document.body.classList.add = () => { h.modal = true; };
  h.document.body.classList.remove = () => { h.modal = false; };
  h.document.body.focus = () => { h.document.activeElement = h.document.body; };
  const container = h.document.createElement('div');
  const get = h.document.getElementById;
  h.document.getElementById = id => id === 'modal-container' ? container : get(id);
  h.modalContainer = container;
  container.querySelector = selector => selector === '.modal-overlay' ? h.currentModal || null : selector.includes('[data-holding-form]') ? h.currentModal?.form || null : h.currentModal?.form.elements[0] || null;
  container.contains = node => Boolean(h.currentModal?.contains(node));
  container.replaceChildren = () => {
    if (container.contains(h.document.activeElement)) h.document.activeElement = h.document.body;
    h.currentModal = null;
    h.forms = [];
  };
  h.app.initModalHandling();
  h.app.initHoldingMutationTracking();
  h.openForm = (id = '1') => {
    const modal = h.document.createElement('div');
    const form = h.document.createElement('form');
    const error = h.document.createElement('p');
    form.dataset.id = id;
    form.matches = () => true;
    form.hasAttribute = () => false;
    form.closest = () => modal;
    form.querySelector = () => error;
    form.error = error;
    form.elements = ['name', 'quantity', 'est_price', 'notes', 'asset_type', 'submit', 'locked'].map(name => {
      const control = h.document.createElement(name === 'notes' ? 'textarea' : name === 'asset_type' ? 'select' : name === 'submit' ? 'button' : 'input');
      control.name = name;
      control.value = control.defaultValue = name === 'quantity' ? '5' : name === 'est_price' ? '0' : `${name} value`;
      control.disabled = name === 'locked';
      if (name === 'asset_type') { control.selectedIndex = 0; control.options = [{ defaultSelected: true, selected: true }]; }
      return control;
    });
    Object.defineProperty(form, 'formData', { get: () => form.elements.filter(control => !control.disabled && control.tagName !== 'BUTTON').map(control => [control.name, control.value]) });
    form.querySelectorAll = selector => selector === 'button' ? form.elements.filter(control => control.tagName === 'BUTTON') : form.elements;
    modal.form = form;
    modal.contains = node => node === modal || node === form || form.elements.includes(node);
    modal.querySelectorAll = () => form.elements;
    h.currentModal = modal;
    h.forms = [form];
    h.emit('htmx:afterSwap', { target: container });
    return form;
  };
  h.submit = form => h.dispatch('submit', { target: form, preventDefault() {} });
  h.close = () => h.dispatch('keydown', { key: 'Escape', target: h.document.activeElement });
  h.startDelete = (id = '1') => {
    const detail = { xhr: {}, requestConfig: { verb: 'delete', path: `/api/holdings/${id}` }, target: {} };
    h.emit('htmx:beforeRequest', detail);
    h.emit('htmx:beforeSend', detail);
    return detail;
  };
  h.endDelete = (detail, successful = true) => h.emit('htmx:afterRequest', { ...detail, successful });
  return h;
}

test('HTMX delete suppresses its target swap and preserves a newer inline draft', () => {
  const h = holdingMutationHarness();
  const request = h.startDelete();
  const edit = h.edit('notes', 'Original note', '1'); edit.input.value = 'Keep this same-row draft';
  const swap = { ...request, shouldSwap: true };
  h.emit('htmx:beforeSwap', swap);
  assert.equal(swap.shouldSwap, false);
  h.endDelete(request); h.runTimers();
  assert.equal(h.reloads, 0);
  assert.equal(edit.td.children[0], edit.input);
  assert.equal(edit.input.value, 'Keep this same-row draft');
  assert.equal(h.document.activeElement, edit.input);
  assert.match(h.notice.children[0].textContent, /Holding deleted.*refresh.*holdings list/);
  const partial = { xhr: {}, requestConfig: { verb: 'get', path: '/partials/holding-form/2' }, shouldSwap: true };
  h.emit('htmx:beforeSwap', partial);
  assert.equal(partial.shouldSwap, true);
});

test('holding reload waits for pending HTMX writes and refreshes once after all succeed', async () => {
  for (const deleteFirst of [true, false]) {
    const h = holdingMutationHarness();
    const request = h.startDelete();
    const edit = h.edit(); h.save(edit, '20');
    if (deleteFirst) h.endDelete(request); else { h.pending[0].resolve({ ok: true }); await flush(); }
    h.runTimers(); assert.equal(h.reloads, 0);
    if (deleteFirst) { h.pending[0].resolve({ ok: true }); await flush(); } else h.endDelete(request);
    h.runTimers(); h.runTimers();
    assert.equal(h.reloads, 1);
  }
});

test('HTMX failures prevent sibling successes from reloading away error feedback', async () => {
  const h = holdingMutationHarness();
  const request = h.startDelete();
  const edit = h.edit(); h.save(edit, '20');
  h.pending[0].resolve({ ok: true }); await flush(); h.runTimers();
  assert.equal(h.reloads, 0);
  h.endDelete(request, false); h.runTimers();
  assert.equal(h.reloads, 0);
  assert.match(h.notice.children[0].textContent, /refresh/);
});

test('a current modal success freezes submitted controls, closes only its modal, and safely refreshes', async () => {
  const h = holdingMutationHarness();
  const trigger = h.document.createElement('button'); trigger.focus();
  const form = h.openForm(); form.elements[1].value = '6';
  h.submit(form); h.submit(form);
  assert.equal(h.pending.length, 1);
  assert.ok(form.elements.every(control => control.disabled));
  assert.equal(JSON.parse(h.pending[0].options.body).quantity, 6, 'data is captured before controls are disabled');
  h.pending[0].resolve({ ok: true }); await flush();
  assert.equal(h.currentModal, null);
  assert.equal(h.modal, false);
  assert.equal(h.document.activeElement, trigger);
  assert.equal(h.reloads, 0);
  h.runTimers(); assert.equal(h.reloads, 1);
});

test('a dismissed modal save cannot reload or steal focus from a newer inline draft', async () => {
  const h = holdingMutationHarness();
  const form = h.openForm(); form.elements[0].value = 'Updated name'; h.submit(form); h.close();
  const edit = h.edit('notes', 'Original'); edit.input.value = 'New unsaved draft';
  h.pending[0].resolve({ ok: true }); await flush(); h.runTimers();
  assert.equal(h.reloads, 0);
  assert.equal(h.document.activeElement, edit.input);
  assert.equal(edit.input.value, 'New unsaved draft');
  assert.equal(edit.td.children[0], edit.input);
});

test('an old modal response cannot close or refocus a replacement modal', async () => {
  const h = holdingMutationHarness();
  const first = h.openForm(); first.elements[0].value = 'Updated name'; h.submit(first); h.close();
  const second = h.openForm('2');
  second.elements[0].value = 'New modal draft';
  const current = h.currentModal;
  h.pending[0].resolve({ ok: true }); await flush(); h.runTimers();
  assert.equal(h.reloads, 0);
  assert.equal(h.currentModal, current);
  assert.equal(h.modal, true);
  assert.equal(h.document.activeElement, second.elements[0]);
  assert.equal(second.elements[0].value, 'New modal draft');
  assert.equal(second.elements[0].disabled, false);
});

test('a pending detached modal write blocks both inline and newer modal success reloads', async () => {
  for (const nextKind of ['inline', 'modal']) {
    const h = holdingMutationHarness();
    const first = h.openForm(); first.elements[0].value = 'Updated name'; h.submit(first); h.close();
    if (nextKind === 'inline') { const edit = h.edit(); h.save(edit, '20'); }
    else { const second = h.openForm('2'); second.elements[0].value = 'Updated second name'; h.submit(second); }
    h.pending[1].resolve({ ok: true }); await flush(); h.runTimers();
    assert.equal(h.reloads, 0);
    h.pending[0].resolve({ ok: true }); await flush(); h.runTimers();
    assert.equal(h.reloads, 1);
  }
});

test('failed modal writes preserve drafts and original disabled states; conflicts require reload', async () => {
  for (const status of [409, 428, 500]) {
    const h = holdingMutationHarness();
    const form = h.openForm(); form.elements[0].value = 'Keep my draft'; h.submit(form);
    const modal = h.currentModal;
    h.pending[0].resolve({ ok: false, status, json: async () => ({ error: 'Save failed' }) }); await flush(); h.runTimers();
    assert.equal(h.currentModal, modal);
    assert.equal(form.elements[0].value, 'Keep my draft');
    assert.ok(form.elements.slice(0, -1).every(control => !control.disabled));
    assert.equal(form.elements.at(-1).disabled, true);
    assert.equal(form.error.hidden, false);
    assert.equal(form.dataset.saving, undefined);
    assert.equal(h.reloads, 0);
    if (status !== 500) {
      assert.match(form.error.textContent, /unsaved edits.*reload before trying again/);
      h.submit(form); assert.equal(h.pending.length, 1);
    } else {
      h.submit(form); assert.equal(h.pending.length, 2);
      h.pending[1].resolve({ ok: true }); await flush(); h.runTimers();
      assert.equal(h.reloads, 0, 'successful retry keeps failure feedback from causing surprise navigation');
    }
  }
});

test('pull refresh completion does not reload during a detached modal write', async () => {
  const h = holdingMutationHarness();
  h.app.initPullToRefresh();
  const target = { closest: () => null, parentElement: h.document.body };
  h.dispatch('touchstart', { target, touches: [{ clientY: 10 }] });
  h.dispatch('touchmove', { target, touches: [{ clientY: 90 }], preventDefault() {} });
  h.dispatch('touchend');
  assert.equal(h.pending.length, 1);
  const form = h.openForm(); form.elements[0].value = 'Updated name'; h.submit(form); h.close();
  h.pending[0].resolve({ ok: true }); await flush(); h.runTimers();
  assert.equal(h.reloads, 0);
  h.pending[1].resolve({ ok: true }); await flush(); h.runTimers();
  assert.equal(h.reloads, 1);
});

test('only the latest modal GET can swap and dismissal invalidates outstanding opens', () => {
  const h = holdingMutationHarness();
  const request = id => ({ xhr: {}, target: h.modalContainer, requestConfig: { verb: 'get', path: `/partials/holding-form/${id}` }, shouldSwap: true });
  const first = request(1); const second = request(2);
  h.emit('htmx:beforeRequest', first); h.emit('htmx:beforeRequest', second);
  h.emit('htmx:beforeSwap', second); assert.equal(second.shouldSwap, true);
  const form = h.openForm('2'); form.elements[0].value = 'Preserve draft';
  h.emit('htmx:beforeSwap', first); assert.equal(first.shouldSwap, false);
  assert.equal(h.currentModal.form, form);
  const pending = request(3); h.emit('htmx:beforeRequest', pending);
  h.emit('htmx:beforeSwap', pending); assert.equal(pending.shouldSwap, false, 'queued repeated opens must not replace an existing editor');
  pending.shouldSwap = true;
  h.close(); h.emit('htmx:beforeSwap', pending);
  assert.equal(pending.shouldSwap, false);
  const next = request(4); h.emit('htmx:beforeRequest', next); h.emit('htmx:beforeSwap', next);
  assert.equal(next.shouldSwap, true, 'a deliberate later open remains available');
});

test('modal PATCH leaves stale untouched values out after an inline save completes', async () => {
  const h = holdingMutationHarness();
  const edit = h.edit('quantity', '5', '1'); h.save(edit, '20');
  h.document.createElement('button').focus(); // Click Edit while the inline PATCH is pending.
  const form = h.openForm('1'); form.elements[3].value = 'Only notes changed';
  h.pending[0].resolve({ ok: true }); await flush(); h.runTimers();
  assert.equal(edit.td.dataset.sortValue, '20'); assert.equal(h.reloads, 0);
  h.submit(form);
  assert.equal(h.pending[1].options.method, 'PATCH');
  assert.deepEqual(JSON.parse(h.pending[1].options.body), { notes: 'Only notes changed' });
  h.pending[1].resolve({ ok: true }); await flush(); h.runTimers();
  assert.equal(h.reloads, 1);
});

test('unchanged modal submits no write while clearing a changed value remains explicit', () => {
  const h = holdingMutationHarness();
  const unchanged = h.openForm(); h.submit(unchanged);
  assert.equal(h.pending.length, 0); assert.equal(h.currentModal, null);
  const edited = h.openForm(); edited.elements[3].value = ''; edited.elements[1].value = '0';
  h.submit(edited);
  assert.deepEqual(JSON.parse(h.pending[0].options.body), { notes: '', quantity: 0 });
});

test('new holding retry retains its idempotency key and independent identical forms get different keys', async () => {
  const h = holdingMutationHarness(); const form = h.openForm('0'); h.submit(form);
  const first = h.pending[0]; assert.equal(first.options.method, 'POST');
  assert.match(first.options.headers['Idempotency-Key'], /^[a-f0-9]{32}$/);
  first.reject(new Error('Response lost')); await flush(); h.submit(form);
  assert.equal(h.pending[1].options.headers['Idempotency-Key'], first.options.headers['Idempotency-Key']);
  assert.equal(h.pending[1].options.body, first.options.body);
  h.pending[1].resolve({ ok: false, status: 422, json: async () => ({ error: 'This Add attempt already created a holding with different details; reload to check it before adding again.' }) });
  await flush(); assert.match(form.error.textContent, /already created/); assert.equal(form.dataset.reloadRequired, undefined);
  h.close(); const another = h.openForm('0'); h.submit(another);
  assert.notEqual(h.pending[2].options.headers['Idempotency-Key'], first.options.headers['Idempotency-Key']);
});

function sourceRefreshHarness() {
  const h = pullHarness();
  const page = element(), row = element(), save = element(), input = element('AAPL'), select = element('Yahoo Finance'), refresh = element();
  refresh.id = 'refresh-prices';
  refresh.closest = () => null;
  refresh.querySelectorAll = () => [];
  page.querySelectorAll = () => [input, select];
  row.querySelectorAll = selector => selector === '.provider-select, .instrument-input' ? [select, input] : [save, input, select];
  row.querySelector = selector => selector === '.provider-select' ? select : selector === '.source-test-result' ? null : input;
  row.dataset.holdingId = '1';
  save.closest = () => row;
  save.hasAttribute = () => true;
  h.document.querySelector = selector => selector === '.sources-page' ? page : null;
  h.app.initSources();
  let reloads = 0;
  h.window.location.reload = () => reloads++;
  return { ...h, row, input, select, save, refresh, reloads: () => reloads,
    click: button => page.click({ target: { closest: () => button } }),
    respond: async (index, ok = true) => { h.pending[index].resolve({ ok, json: async () => ({}) }); await flush(); }
  };
}

test('source refresh preserves mapping drafts made before or during refresh', async () => {
  for (const timing of ['before', 'during']) {
    const h = sourceRefreshHarness();
    if (timing === 'before') h.input.value = 'MSFT';
    h.click(h.refresh);
    assert.equal(h.input.disabled, false);
    if (timing === 'during') h.input.value = 'MSFT';
    await h.respond(0);
    assert.equal(h.reloads(), 0, timing);
    assert.equal(h.input.value, 'MSFT');
    assert.equal(h.refresh.disabled, false);
    h.click(h.save);
    assert.equal(h.pending.length, 2);
    await h.respond(1);
    h.click(h.refresh);
    await h.respond(2);
    assert.equal(h.reloads(), 1, 'saving updates the baseline and permits the next refresh');
  }
});

test('source refresh excludes row saves in both directions and releases locks after failure', async () => {
  const h = sourceRefreshHarness();
  h.input.value = 'MSFT';
  h.click(h.save);
  h.click(h.refresh); h.gesture();
  assert.equal(h.pending.length, 1, 'pending Save blocks both refresh paths');
  h.pending[0].reject(new Error('offline')); await flush();
  assert.equal(h.save.disabled, false);
  assert.equal(h.row.dataset.pending, undefined);
  h.click(h.refresh);
  h.click(h.save); h.click(h.refresh); h.gesture();
  assert.equal(h.pending.length, 2, 'refresh blocks Save and duplicate refresh');
  assert.equal(h.save.disabled, false, 'blocked Save remains available to retry');
  await h.respond(1, false);
  assert.equal(h.refresh.disabled, false);
  assert.equal(h.refresh.dataset.pending, undefined);
  h.click(h.save);
  await h.respond(2);
  h.click(h.refresh);
  await h.respond(3);
  assert.equal(h.reloads(), 1);
});

test('source pull refresh shares exclusion and preserves mapping edits begun in flight', async () => {
  const h = sourceRefreshHarness();
  h.gesture();
  h.click(h.save); h.click(h.refresh);
  assert.equal(h.pending.length, 1);
  h.input.value = 'MSFT';
  await h.respond(0);
  assert.equal(h.reloads(), 0);
  h.gesture();
  assert.equal(h.pending.length, 1, 'dirty mapping blocks another pull');
  h.click(h.save);
  await h.respond(1);
  h.gesture();
  assert.equal(h.pending.length, 3);
  h.pending[2].reject(new Error('offline')); await flush();
  h.click(h.refresh);
  await h.respond(3);
  assert.equal(h.reloads(), 1, 'pull failure releases the shared refresh lock');
});

test('source refresh retains failed-save drafts until a successful retry', async () => {
  const h = sourceRefreshHarness();
  h.input.value = 'MSFT';
  h.click(h.save);
  await h.respond(0, false);
  h.click(h.refresh);
  await h.respond(1);
  assert.equal(h.reloads(), 0);
  assert.equal(h.input.value, 'MSFT');
  h.click(h.save);
  await h.respond(2);
  h.click(h.refresh);
  await h.respond(3);
  assert.equal(h.reloads(), 1);
});
