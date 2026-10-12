const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function loadApp(elements = {}) {
  const source = fs.readFileSync('web/static/app.js', 'utf8').replace(
    '    init,\n    showToast,',
    '    init,\n    safeStorage,\n    saveDisplayCurrency,\n    initSources,\n    makeEditable,\n    initDisclosureKeyboard,\n    initHistory,\n    initPullToRefresh,\n    updateChartColors,\n    getChartOptions,\n    snapshotChartValue,\n    sortTableRows,\n    showToast,');
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
    documentElement: { getAttribute: () => 'light' }
  };
  const context = { document, setTimeout: callback => callback(), window: { scrollY: 0, location: { href: 'https://portfolio.test/holdings?basket=one', assign() {}, reload() {} } }, URL, URLSearchParams,
    getComputedStyle: node => ({ overflowY: node.overflowY || 'visible', getPropertyValue: () => '#123456' }),
    Option: class { constructor(text, value) { this.text = text; this.value = value; } },
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
