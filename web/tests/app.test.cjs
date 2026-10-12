const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function loadApp(elements = {}) {
  const source = fs.readFileSync('web/static/app.js', 'utf8').replace(
    '    init,\n    showToast,',
    '    init,\n    snapshotChartValue,\n    sortTableRows,\n    showToast,');
  const pending = [];
  const document = {
    readyState: 'loading', addEventListener() {},
    getElementById: id => elements[id] || null,
    createTextNode: text => ({ textContent: text }),
    createElement: () => ({ setAttribute() {}, replaceChildren(...children) { this.children = children; },
      appendChild(child) { this.children.push(child); }, addEventListener(type, callback) { this[type] = callback; } }),
    querySelectorAll: () => [],
    documentElement: { getAttribute: () => 'light' }
  };
  const context = { document, window: {}, URLSearchParams, Chart: class { constructor(canvas, config) { this.config = config; } destroy() {} }, fetch: () => new Promise((resolve, reject) => pending.push({ resolve, reject })) };
  vm.runInNewContext(source, context);
  return { app: context.window.PortfolioApp, pending };
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
