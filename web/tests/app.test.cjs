const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function loadApp(elements = {}) {
  const source = fs.readFileSync('web/static/app.js', 'utf8').replace(
    '    init,\n    showToast,',
    '    init,\n    initHistory,\n    initPullToRefresh,\n    updateChartColors,\n    getChartOptions,\n    snapshotChartValue,\n    sortTableRows,\n    showToast,');
  const pending = [];
  const listeners = {};
  const document = {
    body: { style: {}, classList: { contains: () => false } },
    readyState: 'loading', addEventListener(type, callback) { (listeners[type] ||= []).push(callback); },
    querySelector: () => null,
    getElementById: id => elements[id] || null,
    createTextNode: text => ({ textContent: text }),
    createElement: () => ({ setAttribute() {}, replaceChildren(...children) { this.children = children; },
      appendChild(child) { this.children.push(child); }, addEventListener(type, callback) { this[type] = callback; } }),
    querySelectorAll: () => [],
    documentElement: { getAttribute: () => 'light' }
  };
  const context = { document, setTimeout: callback => callback(), window: { scrollY: 0, location: { reload() {} } }, URLSearchParams,
    getComputedStyle: node => ({ overflowY: node.overflowY || 'visible' }),
    Option: class { constructor(text, value) { this.text = text; this.value = value; } },
    Chart: class { constructor(canvas, config) { this.config = config; this.options = config.options; } destroy() {} update() {} },
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
  return { value, children: [], attrs: {}, disabled: false,
    addEventListener(type, callback) { this[type] = callback; },
    replaceChildren(...children) { this.children = children; },
    appendChild(child) { this.children.push(child); },
    setAttribute(key, value) { this.attrs[key] = value; },
    insertRow() { const row = { cells: [], insertCell() { const cell = {}; this.cells.push(cell); return cell; } }; this.children.push(row); return row; },
    add(option) { this.children.push(option); }
  };
}
function historyApp() {
  const elements = Object.fromEntries(['date-range', 'filter-currency', 'holding-select', 'holding-basket', 'holding-location', 'reset-filters', 'first-page', 'prev-page', 'next-page', 'snapshot-value-heading', 'snapshots-tbody', 'snapshots-status', 'page-info'].map(id => [id, element()]));
  elements['date-range'].value = '365';
  elements['filter-currency'].value = 'USD';
  const harness = loadApp(elements);
  harness.app.initHistory();
  return { ...harness, elements, requests: () => harness.pending.filter(p => p.url.includes('/snapshots?')) };
}
const snapshots = count => Array.from({ length: count }, () => ({ timestamp: '2026-10-01', total_value_usd: 10, total_value_try: 300 }));
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
