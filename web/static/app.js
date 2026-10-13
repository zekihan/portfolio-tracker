// Storage can be unavailable in private or restricted browser contexts.
// Keep preferences usable for this page even when persistence is denied.
function safeStorage(name) {
  const fallback = new Map();
  return {
    getItem(key) {
      if (fallback.has(key)) return fallback.get(key);
      try { return window[name].getItem(key); } catch { return null; }
    },
    setItem(key, value) {
      fallback.set(key, String(value));
      try { window[name].setItem(key, value); } catch { /* Page-local preference remains usable. */ }
    },
    removeItem(key) {
      fallback.set(key, null);
      try { window[name].removeItem(key); } catch { /* Persistence is optional. */ }
    }
  };
}
const localPreferences = safeStorage('localStorage');
const sessionPreferences = safeStorage('sessionStorage');

// This token belongs to the data initially rendered on this page. Never replace
// it from asynchronous reads: their IDs may refer to a restored portfolio.
const portfolioGeneration = document.documentElement.dataset?.portfolioGeneration || '';
function portfolioHeaders(headers = {}) {
  return { ...headers, 'X-Portfolio-Generation': portfolioGeneration };
}
function portfolioErrorMessage(response, fallback) {
  return response.status === 409 || response.status === 428 ?
    'Portfolio changed or this page is out of date. Keep any unsaved edits, then reload before trying again.' : fallback;
}


// Send HTMX request bodies as JSON: the API handlers only accept application/json.
if (window.htmx) {
  htmx.defineExtension('json-body', {
    onEvent: (name, evt) => {
      if (name === 'htmx:configRequest') {
        evt.detail.headers['Content-Type'] = 'application/json';
        evt.detail.headers['X-Portfolio-Generation'] = portfolioGeneration;
      }
    },
    encodeParameters: (xhr, parameters) => {
      xhr.overrideMimeType('text/json');
      return JSON.stringify(parameters);
    },
  });
}

/**
 * Portfolio Tracker - Main Application JavaScript
 * Handles HTMX interactions, Chart.js, theme, and UI state
 */

const PortfolioApp = (function() {
  'use strict';

  // State
  let state = {
    displayCurrency: 'TRY',
    theme: 'system',
    charts: {
      portfolio: null,
      allocation: null,
      holding: null
    }
  };

  let portfolioRequest = 0;
  let allocationRequest = 0;
  let historyMutationActive = false;
  let currencyUpdateActive = false;
  // Sources and mobile refresh share a lock; mapping drafts live outside forms.
  let priceRefreshActive = false;
  let sourceRequestsPending = 0;
  const sourceBaselines = new Map();
  const sourceDraftsPending = () => [...sourceBaselines].some(([control, value]) => control.value !== value);

  // Chart color palette
  const CHART_COLORS = [
    '#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#06b6d4',
    '#f97316', '#ec4899', '#14b8a6', '#6366f1', '#84cc16', '#eab308'
  ];

  // Initialize app
  function init(options = {}) {
    state.displayCurrency = options.displayCurrency || document.documentElement.dataset.currency || 'TRY';
    state.theme = localPreferences.getItem('theme') || document.documentElement.dataset.themePreference || 'system';
    
    applyTheme(state.theme);
    initThemeToggle();
    initCurrencySwitcher();
    initDisplaySettings();
    initSources();
    initProviderHealth();
    initMobileMenu();
    initDropdowns();
    initDisclosureKeyboard();
    initToasts();
    initCharts();
    initHistory();
    initTableInteractions();
    initHoldingFilters();
    initModalHandling();
    initPullToRefresh();
    
    // Listen for HTMX events
    document.body.addEventListener('htmx:confirm', async event => {
      if (!event.detail.question) return;
      event.preventDefault();
      if (await confirmAction(event.detail.question, 'Delete holding')) event.detail.issueRequest(true);
    });
    document.body.addEventListener('htmx:beforeRequest', onHtmxBeforeRequest);
    document.body.addEventListener('portfolio-updated', onPortfolioUpdated);
    initHoldingMutationTracking();
    document.body.addEventListener('htmx:responseError', event => showToast(portfolioErrorMessage(event.detail.xhr || {}, 'The request failed. Please try again.'), 'error'));
    document.body.addEventListener('htmx:sendError', () => showToast('Unable to connect to the server.', 'error'));
    
    // Load initial chart data
    loadPortfolioChart('1y');
    loadAllocationChart('type');
  }

  // Theme handling
  function applyTheme(theme) {
    const root = document.documentElement;
    if (theme === 'system') {
      theme = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    }
    root.setAttribute('data-theme', theme);
    state.theme = theme;
    updateChartColors();
  }

  function initThemeToggle() {
    const toggle = document.getElementById('themeToggle');
    if (!toggle) return;

    const updateToggle = () => {
      const isDark = document.documentElement.getAttribute('data-theme') === 'dark';
      toggle.setAttribute('aria-pressed', isDark);
    };

    toggle.addEventListener('click', () => {
      const current = document.documentElement.getAttribute('data-theme');
      const next = current === 'dark' ? 'light' : 'dark';
      localPreferences.setItem('theme', next);
      applyTheme(next);
      const themeSelect = document.getElementById('dark-mode');
      if (themeSelect) themeSelect.value = next;
      updateToggle();
    });

    updateToggle();

    // Listen for system theme changes
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', (e) => {
      if (localPreferences.getItem('theme') === 'system' || !localPreferences.getItem('theme')) {
        applyTheme('system');
        updateToggle();
      }
    });
  }

  function confirmAction(message, action) {
    const dialog = document.getElementById('confirm-action');
    if (dialog.open) return Promise.resolve(false);
    document.getElementById('confirm-action-title').textContent = action;
    document.getElementById('confirm-action-message').textContent = message;
    document.getElementById('confirm-action-submit').textContent = action;
    dialog.returnValue = 'cancel';
    return new Promise(resolve => {
      dialog.addEventListener('close', () => resolve(dialog.returnValue === 'confirm'), { once: true });
      dialog.showModal();
    });
  }

  function initDisplaySettings() {
    const backupStatus = document.getElementById('backup-status');
    if (backupStatus) {
      backupStatus.textContent = sessionPreferences.getItem('portfolio-restore-success') || '';
      sessionPreferences.removeItem('portfolio-restore-success');
    }
    const clear = document.getElementById('clear-all-data');
    clear?.addEventListener('click', async () => {
      if (!await confirmAction('This will delete all holdings, snapshots, and settings. Download a backup first to keep the current data.', 'Clear all data')) return;
      clear.disabled = true;
      try {
        const response = await fetch('/api/settings/clear-all', { method: 'DELETE', headers: portfolioHeaders() });
        if (!response.ok) throw new Error(portfolioErrorMessage(response, 'Unable to clear data'));
        localPreferences.removeItem('theme');
        sessionPreferences.removeItem('portfolio-holding-filters');
        window.location.assign('/settings');
      } catch (error) {
        clear.disabled = false;
        showToast(error.message, 'error');
      }
    });
    const currency = document.getElementById('display-currency');
    currency?.addEventListener('change', () => saveDisplayCurrency(currency.value));
    const theme = document.getElementById('dark-mode');
    if (theme) theme.value = localPreferences.getItem('theme') || document.documentElement.dataset.themePreference || 'system';
    theme?.addEventListener('change', async () => {
      theme.disabled = true;
      try {
        const response = await fetch('/api/settings/dark-mode', {
          method: 'PATCH', headers: portfolioHeaders({ 'Content-Type': 'application/json' }),
          body: JSON.stringify({ mode: theme.value })
        });
        if (!response.ok) throw new Error(portfolioErrorMessage(response, 'Unable to save theme'));
        localPreferences.setItem('theme', theme.value);
        applyTheme(theme.value);
        document.getElementById('themeToggle')?.setAttribute('aria-pressed', document.documentElement.dataset.theme === 'dark');
        showToast('Theme saved', 'success');
      } catch (error) {
        theme.value = localPreferences.getItem('theme') || 'system';
        showToast(error.message, 'error');
      } finally { theme.disabled = false; }
    });
  }

  function initSources() {
    const page = document.querySelector('.sources-page');
    if (!page) return;
    page.querySelectorAll('.provider-select, .instrument-input').forEach(control => sourceBaselines.set(control, control.value));
    async function request(url, method, data) {
      const response = await fetch(url, {
        method, headers: portfolioHeaders({ 'Content-Type': 'application/json' }),
        ...(data ? { body: JSON.stringify(data) } : {})
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok || body.success === false) throw new Error(portfolioErrorMessage(response, body.error || 'Request failed'));
      return body;
    }
    document.querySelector('.sources-page').addEventListener('click', async (event) => {
      const button = event.target.closest('[data-test-provider], [data-test-mapping], [data-save-mapping], #refresh-prices');
      if (!button || button.disabled) return;
      const refresh = button.id === 'refresh-prices';
      if (priceRefreshActive || (refresh && sourceRequestsPending)) {
        showToast('Wait for the current source request to finish, then try again.', 'info');
        return;
      }
      const scope = button.closest('tr') || button;
      if (scope.dataset.pending) return;
      scope.dataset.pending = 'true';
      if (refresh) priceRefreshActive = true;
      else sourceRequestsPending++;
      const controls = [button, ...scope.querySelectorAll('button, input, select')];
      const disabledBefore = controls.map(control => control.disabled);
      controls.forEach(control => { control.disabled = true; });
      scope.setAttribute('aria-busy', 'true');
      const output = button.closest('tr')?.querySelector('.source-test-result');
      if (output) {
        output.textContent = 'Checking…';
        output.classList.remove('text-error');
      }
      try {
        if (refresh) {
          await request('/api/sources/refresh', 'POST');
          if (!sourceDraftsPending() && !sourceRequestsPending) window.location.reload();
          else showToast('Prices refreshed. Your unsaved source edits are still here; save them before refreshing the page.', 'info');
          return;
        }
        const row = button.closest('tr');
        const provider = button.dataset.testProvider || row.querySelector('.provider-select').value;
        const instrumentId = row.querySelector('.instrument-input, .provider-test-instrument').value.trim();
        if (button.hasAttribute('data-save-mapping')) {
          await request(`/api/holdings/${row.dataset.holdingId}/provider`, 'PUT', { provider, instrumentId });
          row.querySelectorAll('.provider-select, .instrument-input').forEach(control => sourceBaselines.set(control, control.value));
          if (output) output.textContent = 'Price source saved';
          showToast('Price source saved', 'success');
        } else {
          if (!provider || !instrumentId) throw new Error('Choose a provider and enter an instrument ID first.');
          const result = await request('/api/sources/test', 'POST', { provider, instrumentId });
          const value = Number(result.price).toLocaleString(undefined, { maximumFractionDigits: 12 });
          const message = `${value} ${result.currency} · ${result.source || provider}`;
          if (output) output.textContent = `${instrumentId}: ${message}`;
          showToast(message, 'success');
        }
      } catch (error) {
        if (output) {
          output.textContent = error.message;
          output.classList.add('text-error');
        }
        showToast(error.message, 'error');
      } finally {
        if (refresh) priceRefreshActive = false;
        else sourceRequestsPending--;
        controls.forEach((control, index) => { control.disabled = disabledBefore[index]; });
        delete scope.dataset.pending;
        scope.setAttribute('aria-busy', 'false');
      }
    });

  }

  function initProviderHealth() {
    const button = document.getElementById('refresh-provider-health');
    if (!button) return;
    const refresh = async () => {
      if (button.disabled) return;
      button.disabled = true;
      try {
        const response = await fetch('/api/sources/providers');
        if (!response.ok) throw new Error('Unable to load provider health');
        const providers = await response.json();
        const container = document.getElementById('provider-health');
        container.replaceChildren();
        providers.forEach(provider => {
          const row = document.createElement('div');
          row.className = 'provider-row';
          row.classList.toggle('failed', provider.Status.includes('FAILED'));
          row.classList.toggle('warning', provider.Status.includes('Errors'));
          const info = document.createElement('div');
          info.className = 'provider-info';
          const name = document.createElement('span');
          name.className = 'provider-name';
          name.textContent = provider.Name;
          const status = document.createElement('span');
          status.className = 'provider-status';
          status.textContent = provider.Status;
          info.append(name, status);
          const time = document.createElement('div');
          time.className = 'provider-time';
          const lastOK = new Date(provider.LastOK);
          time.textContent = !Number.isNaN(lastOK.getTime()) && lastOK.getUTCFullYear() > 1 ?
            `${lastOK.toISOString().replace('T', ' ').slice(0, 16)} UTC` : 'Never';
          row.append(info, time);
          container.appendChild(row);
        });
      } catch (error) { showToast(error.message, 'error'); }
      finally { button.disabled = false; }
    };
    button.addEventListener('click', refresh);
    setInterval(refresh, 60000);
  }

  // Currency switcher
  function initCurrencySwitcher() {
    const btn = document.querySelector('.currency-btn');
    const menu = document.getElementById('currency-menu');
    if (!btn || !menu) return;

    // Load currency options
    loadCurrencyOptions();

    // Toggle menu
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const expanded = btn.getAttribute('aria-expanded') === 'true';
      btn.setAttribute('aria-expanded', !expanded);
      menu.setAttribute('aria-hidden', expanded);
    });

    // Close on outside click
    document.addEventListener('click', (e) => {
      if (!btn.contains(e.target) && !menu.contains(e.target)) {
        btn.setAttribute('aria-expanded', 'false');
        menu.setAttribute('aria-hidden', 'true');
      }
    });
  }

  function loadCurrencyOptions() {
    const menu = document.getElementById('currency-menu');
    if (!menu) return;

    menu.replaceChildren();
    [
      { currency: 'TRY', label: 'Turkish Lira (TRY)' },
      { currency: 'USD', label: 'US Dollar (USD)' }
    ].forEach(({ currency, label }) => {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = `dropdown-item ${state.displayCurrency === currency ? 'active' : ''}`;
      button.dataset.currency = currency;
      button.textContent = label;
      menu.appendChild(button);
    });

    menu.querySelectorAll('button').forEach(button => {
      button.addEventListener('click', () => saveDisplayCurrency(button.dataset.currency));
    });
  }

  async function saveDisplayCurrency(currency) {
    if (currencyUpdateActive) return;
    currencyUpdateActive = true;
    const controls = [...document.querySelectorAll('#currency-menu button, #display-currency')];
    controls.forEach(control => { control.disabled = true; });
    try {
      const response = await fetch('/api/settings/currency', {
        method: 'PATCH', headers: portfolioHeaders({ 'Content-Type': 'application/json' }),
        body: JSON.stringify({ currency })
      });
      if (!response.ok) throw new Error(portfolioErrorMessage(response, 'Unable to save display currency. Please try again.'));
      closeDropdown();
      const url = new URL(window.location.href);
      url.searchParams.set('currency', currency);
      window.location.assign(url.href);
    } catch (error) {
      const select = document.getElementById('display-currency');
      if (select) select.value = state.displayCurrency;
      currencyUpdateActive = false;
      controls.forEach(control => { control.disabled = false; });
      showToast(error.message || 'Unable to save display currency. Please try again.', 'error');
    }
  }

  function closeDropdown() {
    const btn = document.querySelector('.currency-btn');
    const menu = document.getElementById('currency-menu');
    if (btn && menu) {
      btn.setAttribute('aria-expanded', 'false');
      menu.setAttribute('aria-hidden', 'true');
    }
  }

  // Mobile menu
  function initMobileMenu() {
    const toggle = document.querySelector('.mobile-menu-toggle');
    const nav = document.querySelector('.header-nav');
    if (!toggle || !nav) return;

    toggle.addEventListener('click', () => {
      const expanded = toggle.getAttribute('aria-expanded') === 'true';
      toggle.setAttribute('aria-expanded', !expanded);
      nav.classList.toggle('is-open', !expanded);
    });
  }

  function positionMenu(menu, toggle) {
    const rect = toggle.getBoundingClientRect();
    menu.style.position = 'fixed';
    menu.style.right = 'auto';
    menu.style.bottom = 'auto';
    menu.style.left = `${Math.max(8, Math.min(rect.right - menu.offsetWidth, window.innerWidth - menu.offsetWidth - 8))}px`;
    menu.style.top = `${Math.max(8, Math.min(rect.bottom + 4, window.innerHeight - menu.offsetHeight - 8))}px`;
  }

  // Dropdowns
  function initDropdowns() {
    const closeMenus = (except) => {
      document.querySelectorAll('.dropdown-toggle[aria-expanded="true"]').forEach(toggle => {
        if (toggle === except) return;
        toggle.setAttribute('aria-expanded', 'false');
        toggle.closest('.dropdown').querySelector('.dropdown-menu')?.setAttribute('aria-hidden', 'true');
      });
    };
    document.addEventListener('click', event => {
      const toggle = event.target.closest('.dropdown-toggle');
      closeMenus(toggle);
      if (!toggle) return;
      const menu = toggle.closest('.dropdown').querySelector('.dropdown-menu');
      const open = toggle.getAttribute('aria-expanded') !== 'true';
      toggle.setAttribute('aria-expanded', open);
      menu.setAttribute('aria-hidden', !open);
      if (open) positionMenu(menu, toggle);
    });
    window.addEventListener('resize', () => closeMenus());
  }

  function initDisclosureKeyboard() {
    document.addEventListener('keydown', event => {
      if (event.key !== 'Escape') return;
      const toggles = [...document.querySelectorAll('.currency-btn, #column-toggle, .dropdown-toggle')];
      const active = toggles.find(toggle => {
        const menu = toggle.classList.contains('dropdown-toggle') ?
          toggle.closest('.dropdown')?.querySelector('.dropdown-menu') :
          document.getElementById(toggle.getAttribute('aria-controls') || 'column-menu');
        return menu && (menu.contains(event.target) || toggle === event.target) &&
          toggle.getAttribute('aria-expanded') === 'true';
      });
      toggles.forEach(toggle => {
        const menu = toggle.classList.contains('dropdown-toggle') ?
          toggle.closest('.dropdown')?.querySelector('.dropdown-menu') :
          document.getElementById(toggle.getAttribute('aria-controls') || 'column-menu');
        toggle.setAttribute('aria-expanded', 'false');
        menu?.setAttribute('aria-hidden', 'true');
      });
      if (active) { event.preventDefault(); active.focus(); }
    });
  }

  // Toasts
  function initToasts() {
    const container = document.getElementById('toast-container');
    if (!container) return;

    // Listen for toast events from HTMX
    document.body.addEventListener('show-toast', (e) => {
      showToast(e.detail.message, e.detail.type || 'info');
    });
  }

  function showToast(message, type = 'info') {
    const container = document.getElementById('toast-container');
    if (!container) return;

    const toast = document.createElement('div');
    toast.className = `toast ${type}`;
    toast.innerHTML = `
      <span class="toast-message">${escapeHtml(message)}</span>
      <button class="toast-close" aria-label="Dismiss">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <line x1="18" y1="6" x2="6" y2="18"></line>
          <line x1="6" y1="6" x2="18" y2="18"></line>
        </svg>
      </button>
    `;

    toast.querySelector('.toast-close').addEventListener('click', () => {
      toast.style.animation = 'slideIn 0.2s ease reverse';
      setTimeout(() => toast.remove(), 200);
    });

    container.appendChild(toast);

    // Auto dismiss
    setTimeout(() => {
      if (toast.parentNode) {
        toast.style.animation = 'slideIn 0.2s ease reverse';
        setTimeout(() => toast.remove(), 200);
      }
    }, 5000);
  }

  // Charts
  function initCharts() {
    document.querySelectorAll('.chart-tab').forEach(tab => {
      tab.addEventListener('click', () => loadPortfolioChart(tab.dataset.range));
    });
    document.querySelectorAll('.alloc-tab').forEach(tab => {
      tab.addEventListener('click', () => loadAllocationChart(tab.dataset.alloc));
    });
  }

  function chartStatus(id, message, retry) {
    const canvas = document.getElementById(id);
    if (!canvas) return;
    let status = document.getElementById(`${id}-status`);
    if (!status) {
      status = document.createElement('p');
      status.id = `${id}-status`;
      status.setAttribute('role', 'status');
      status.setAttribute('aria-live', 'polite');
      canvas.parentNode.before(status);
    }
    status.replaceChildren(document.createTextNode(message));
    status.hidden = !message;
    if (retry) {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'btn btn-sm btn-secondary';
      button.textContent = 'Retry';
      button.addEventListener('click', retry);
      status.appendChild(button);
    }
  }

  function snapshotChartValue(point, currency) {
    // Older snapshots can contain partial totals after a failed provider lookup.
    // Keep an explicit gap rather than drawing a misleading portfolio loss.
    return point.failed_count > 0 || point.valuation_complete === false || point.valuation_available === false ? null :
      (currency === 'USD' ? point.value_usd : point.value_try);
  }

  function loadPortfolioChart(range) {
    if (historyMutationActive) return;
    if (!document.getElementById('portfolio-chart')) return;
    const daysMap = { '1y': 365, '6m': 180, '3m': 90, '1m': 30 };
    const days = document.getElementById('date-range')?.value || daysMap[range] || 365;
    
    const request = ++portfolioRequest;
    state.charts.portfolio?.destroy();
    state.charts.portfolio = null;
    chartStatus('portfolio-chart', 'Loading portfolio history…');
    fetch(`/api/history/chart?days=${days}`)
      .then(response => {
        if (!response.ok) throw new Error('Unable to load portfolio history');
        return response.json();
      })
      .then(data => { if (request === portfolioRequest) renderPortfolioChart(data, range); })
      .catch(() => {
        if (request === portfolioRequest) chartStatus('portfolio-chart',
          'Portfolio history unavailable. ', () => loadPortfolioChart(range));
      });
  }

  function renderPortfolioChart(data, range) {
    const ctx = document.getElementById('portfolio-chart');
    if (!ctx) return;

    if (state.charts.portfolio) {
      state.charts.portfolio.destroy();
    }

    const labels = data.map(d => new Date(d.timestamp).toLocaleDateString(undefined, { month: 'short', day: 'numeric' }));
    const valuesTry = data.map(d => snapshotChartValue(d, 'TRY'));
    const valuesUsd = data.map(d => snapshotChartValue(d, 'USD'));
    const hasFailures = data.some(d => d.failed_count > 0 || d.valuation_complete === false);
    chartStatus('portfolio-chart', hasFailures ?
      'Snapshots with failed prices are omitted from the line because their totals may be incomplete.' :
      data.length ? '' : 'No snapshots in this date range.');

    const isUSD = (document.getElementById('filter-currency')?.value || state.displayCurrency) === 'USD';
    const values = isUSD ? valuesUsd : valuesTry;
    const currency = isUSD ? 'USD' : 'TRY';

    state.charts.portfolio = new Chart(ctx, {
      type: 'line',
      data: {
        labels,
        datasets: [{
          label: `Portfolio Value (${currency})`,
          data: values,
          borderColor: CHART_COLORS[0],
          backgroundColor: 'transparent',
          borderWidth: 2,
          fill: false,
          tension: 0.1,
          pointRadius: 0,
          pointHoverRadius: 4,
          spanGaps: false
        }]
      },
      options: getChartOptions(currency)
    });

    // Update active tab
    document.querySelectorAll('.chart-tab').forEach(tab => {
      tab.classList.toggle('active', tab.dataset.range === range);
      tab.setAttribute('aria-selected', tab.dataset.range === range);
    });
  }

  function loadAllocationChart(type) {
    if (!document.getElementById('allocation-chart')) return;
    const params = new URLSearchParams({ type, currency: state.displayCurrency });
    const request = ++allocationRequest;
    state.charts.allocation?.destroy();
    state.charts.allocation = null;
    chartStatus('allocation-chart', 'Loading allocation…');
    fetch(`/api/dashboard/allocation?${params}`)
      .then(response => {
        if (!response.ok) throw new Error('Unable to load allocation');
        return response.json();
      })
      .then(data => { if (request === allocationRequest) renderAllocationChart(data, type); })
      .catch(() => {
        if (request === allocationRequest) chartStatus('allocation-chart',
          'Allocation unavailable. ', () => loadAllocationChart(type));
      });
  }

  function renderAllocationChart(data, type) {
    const ctx = document.getElementById('allocation-chart');
    if (!ctx) return;

    if (state.charts.allocation) {
      state.charts.allocation.destroy();
    }

    const allocationKey = { location: 'by_loc', currency: 'by_curr' }[type] || `by_${type}`;
    const allocation = data[allocationKey] || {};
    const labels = Object.keys(allocation);
    const values = Object.values(allocation);
    const total = values.reduce((a, b) => a + b, 0);
    chartStatus('allocation-chart', data.valuation_complete === false ?
      `Partial allocation: ${data.valued_count} holdings valued; ${data.unvalued_count} unavailable. Percentages cover valued holdings only.` :
      total > 0 ? '' : 'No positive valued holdings to display.');

    // Filter out zero values
    const filtered = labels.map((label, i) => ({ label, value: values[i] }))
      .filter(d => d.value > 0)
      .sort((a, b) => b.value - a.value);

    // Keep top 7, group rest as "Other"
    let chartLabels = filtered.slice(0, 7).map(d => d.label);
    let chartValues = filtered.slice(0, 7).map(d => d.value);
    const otherValue = filtered.slice(7).reduce((a, b) => a + b.value, 0);
    if (otherValue > 0) {
      chartLabels.push('Other');
      chartValues.push(otherValue);
    }

    const percentages = chartValues.map(v => ((v / total) * 100).toFixed(1));

    state.charts.allocation = new Chart(ctx, {
      type: 'doughnut',
      data: {
        labels: chartLabels,
        datasets: [{
          data: chartValues,
          backgroundColor: CHART_COLORS.slice(0, chartLabels.length),
          borderWidth: 2,
          borderColor: getComputedStyle(document.documentElement).getPropertyValue('--bg-card').trim()
        }]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: {
            position: 'right',
            labels: {
              usePointStyle: true,
              padding: 16,
              color: getChartOptions(state.displayCurrency).scales.x.ticks.color,
              font: { size: 12 },
              generateLabels: function(chart) {
                const original = Chart.overrides.doughnut.plugins.legend.labels.generateLabels(chart);
                return original.map((item, i) => ({
                  ...item,
                  text: `${item.text} ${percentages[i]}%`
                }));
              }
            }
          },
          tooltip: {
            ...getChartOptions(state.displayCurrency).plugins.tooltip,
            callbacks: {
              label: function(context) {
                const value = context.raw;
                const pct = ((value / total) * 100).toFixed(1);
                return `${context.label}: ${formatCurrency(value, state.displayCurrency)} (${pct}%)`;
              }
            }
          }
        }
      }
    });

    // Update active tab
    document.querySelectorAll('.alloc-tab').forEach(tab => {
      tab.classList.toggle('active', tab.dataset.alloc === type);
      tab.setAttribute('aria-selected', tab.dataset.alloc === type);
    });
  }

  function initHistory() {
    const range = document.getElementById('date-range');
    if (!range) return;
    const currency = document.getElementById('filter-currency');
    const select = document.getElementById('holding-select');
    const basket = document.getElementById('holding-basket');
    const location = document.getElementById('holding-location');
    let holdings = [];
    let offset = 0;
    let holdingRequest = 0;
    let snapshotRequest = 0;
    let snapshotPage = null;
    let deletePending = false;
    let snapshotLoading = false;
    const clearHistory = document.getElementById('clear-history');
    const deleteStatus = document.getElementById('history-delete-status');
    const historyControls = [range, currency, select, basket, location, document.getElementById('reset-filters')];
    const updateCharts = () => { loadPortfolioChart(); loadHolding(); loadSnapshots(0); };
    range.addEventListener('change', updateCharts);
    currency.addEventListener('change', updateCharts);
    document.getElementById('reset-filters').addEventListener('click', () => {
      range.value = '365';
      currency.value = state.displayCurrency;
      updateCharts();
    });
    const updateHoldings = () => {
      const selected = select.value;
      select.replaceChildren(new Option('Select a holding...', ''));
      holdings.filter(holding => (!basket.value || holding.basket === basket.value) &&
        (!location.value || holding.location === location.value)).forEach(holding => {
        select.add(new Option(holding.name, String(holding.id)));
      });
      select.value = selected;
      loadHolding();
    };
    basket.addEventListener('change', updateHoldings);
    location.addEventListener('change', updateHoldings);
    select.addEventListener('change', loadHolding);
    fetch('/api/holdings').then(response => {
      if (!response.ok) throw new Error('Unable to load holdings');
      return response.json();
    }).then(data => { holdings = data || []; updateHoldings(); })
      .catch(error => showToast(error.message, 'error'));

    async function loadHolding() {
      if (historyMutationActive) return;
      const request = ++holdingRequest;
      state.charts.holding?.destroy();
      state.charts.holding = null;
      if (!select.value) {
        chartStatus('holding-chart', 'Select a holding to view its history.');
        return;
      }
      chartStatus('holding-chart', 'Loading holding history…');
      try {
        const response = await fetch(`/api/history/holding/${select.value}?days=${range.value}`);
        if (!response.ok) throw new Error('Unable to load holding history');
        const data = await response.json();
        if (request !== holdingRequest) return;
        chartStatus('holding-chart', data.some(point => point.valuation_available === false) ?
          'Unavailable historical valuations are shown as gaps.' :
          data.length ? '' : 'No snapshots for this holding in this date range.');
        state.charts.holding = new Chart(document.getElementById('holding-chart'), {
          type: 'line',
          data: {
            labels: data.map(point => new Date(point.timestamp).toLocaleDateString()),
            datasets: [{ label: `Holding value (${currency.value})`,
              data: data.map(point => snapshotChartValue(point, currency.value)),
              borderColor: CHART_COLORS[0], tension: 0.1, spanGaps: false }]
          },
          options: getChartOptions(currency.value)
        });
      } catch {
        if (request === holdingRequest) chartStatus('holding-chart',
          'Holding history unavailable. ', loadHolding);
      }
    }

    const first = document.getElementById('first-page');
    const previous = document.getElementById('prev-page');
    const next = document.getElementById('next-page');
    first.addEventListener('click', () => loadSnapshots(0));
    previous.addEventListener('click', () => loadSnapshots(Math.max(0, offset - 50)));
    next.addEventListener('click', () => loadSnapshots(offset + 50));
    function updateHistoryControls() {
      historyControls.forEach(control => { control.disabled = deletePending; });
      if (clearHistory) clearHistory.disabled = deletePending;
      document.querySelectorAll('.snapshot-delete').forEach(button => { button.disabled = deletePending; });
      first.disabled = previous.disabled = deletePending || snapshotLoading || !snapshotPage || offset === 0;
      next.disabled = deletePending || snapshotLoading || !snapshotPage?.hasNext;
    }

    clearHistory?.addEventListener('click', () => deleteSnapshots());
    async function deleteSnapshots(snapshot, trigger = clearHistory) {
      if (deletePending) return;
      deletePending = true;
      updateHistoryControls();
      const all = !snapshot;
      const action = all ? 'Clear history' : 'Delete snapshot';
      const message = all ?
        'Permanently delete ALL snapshots across every date range, including history outside the selected range? Holdings and settings will stay unchanged. Download a backup first if needed. Hourly snapshots will resume automatically; correct or remove the wrong holding separately to keep it out of future snapshots.' :
        `Permanently delete the snapshot from ${new Date(snapshot.timestamp).toISOString().replace('T', ' ').slice(0, 19)} UTC and its holding history? Holdings and settings will stay unchanged. This cannot be undone. Download a backup first if needed.`;
      try {
        if (!await confirmAction(message, action)) return;
        historyMutationActive = true;
        // Reads started before the mutation must never resurrect deleted history.
        ++snapshotRequest;
        ++holdingRequest;
        ++portfolioRequest;
        snapshotLoading = false;
        document.getElementById('snapshots-tbody').setAttribute('aria-busy', 'false');
        deleteStatus.textContent = all ? 'Clearing history…' : 'Deleting snapshot…';
        const response = await fetch(all ? '/api/history/snapshots' : `/api/history/snapshots/${snapshot.id}`, {
          method: 'DELETE', headers: portfolioHeaders(all ? { 'X-Clear-History-Confirm': 'clear-history' } : {})
        });
        if (!response.ok) throw new Error(portfolioErrorMessage(response, 'History was not deleted. Please try again.'));
        historyMutationActive = false;
        if (all) {
          document.getElementById('snapshots-tbody').replaceChildren();
          // Clearing history advances the portfolio epoch. Rebuild the entire
          // page rather than adopting a token from an arbitrary later read.
          const url = new URL(window.location.href);
          url.searchParams.set('days', range.value);
          url.searchParams.set('currency', currency.value);
          window.location.assign(url.href);
        }
        else Array.from(document.getElementById('snapshots-tbody').children).forEach(row => {
          if (row.dataset.snapshotId === String(snapshot.id)) row.remove();
        });
        deleteStatus.textContent = all ? 'History cleared. Holdings and settings are unchanged. Hourly snapshots will resume automatically.' : 'Snapshot deleted. Holdings and settings are unchanged.';
        loadPortfolioChart();
        loadHolding();
        await loadSnapshots(all ? 0 : offset);
      } catch (error) {
        historyMutationActive = false;
        deleteStatus.textContent = error.message?.includes('Portfolio changed') ? error.message : 'Unable to confirm deletion. Check the refreshed history before trying again.';
        // A lost response may hide a successful DELETE. Reconcile with the server,
        // and replace any interrupted loading states, keeping rows until reads succeed.
        loadPortfolioChart();
        loadHolding();
        await loadSnapshots(offset);
      } finally {
        historyMutationActive = false;
        deletePending = false;
        updateHistoryControls();
        // The dialog's original trigger was disabled while it closed. Restore
        // keyboard focus only if the user has not moved elsewhere meanwhile.
        if (!document.activeElement || document.activeElement === document.body ||
            document.activeElement === document.getElementById('confirm-action')) {
          (trigger?.isConnected !== false ? trigger : clearHistory)?.focus();
        }
      }
    }

    async function loadSnapshots(nextOffset) {
      if (historyMutationActive) return;
      const request = ++snapshotRequest;
      const filter = `${range.value}:${currency.value}`;
      const body = document.getElementById('snapshots-tbody');
      const info = document.getElementById('page-info');
      const status = document.getElementById('snapshots-status');
      // Navigation belongs only to a successfully loaded page of these filters.
      if (snapshotPage?.filter !== filter) {
        snapshotPage = null;
        offset = 0;
        body.replaceChildren();
        info.textContent = '';
      }
      snapshotLoading = true;
      status.replaceChildren(document.createTextNode('Loading snapshots…'));
      body.setAttribute('aria-busy', 'true');
      [first, previous, next].forEach(button => { button.disabled = true; });
      try {
        const response = await fetch(`/api/history/snapshots?limit=51&offset=${nextOffset}&days=${range.value}`);
        if (!response.ok) throw new Error('Unable to load snapshots');
        const snapshots = (await response.json()) || [];
        if (request !== snapshotRequest) return;
        if (!snapshots.length && nextOffset > 0) {
          await loadSnapshots(Math.max(0, nextOffset - 50));
          return;
        }
        document.getElementById('snapshot-value-heading').textContent = `Value (${currency.value})`;
        body.replaceChildren();
        snapshots.slice(0, 50).forEach(snapshot => {
          const row = body.insertRow();
          row.dataset.snapshotId = String(snapshot.id);
          [new Date(snapshot.timestamp).toISOString().replace('T', ' ').slice(0, 19),
            (snapshot.failed_count > 0 ? 'Possibly incomplete: ' : '') + formatCurrency(currency.value === 'USD' ? snapshot.total_value_usd : snapshot.total_value_try, currency.value), snapshot.usd_try_rate,
            snapshot.stale_count, snapshot.failed_count].forEach(value => {
            row.insertCell().textContent = value;
          });
          const button = document.createElement('button');
          button.type = 'button';
          button.className = 'btn btn-sm btn-danger snapshot-delete';
          button.textContent = 'Delete';
          button.disabled = deletePending;
          button.setAttribute('aria-label', `Delete snapshot from ${new Date(snapshot.timestamp).toISOString()} UTC`);
          button.addEventListener('click', () => deleteSnapshots(snapshot, button));
          row.insertCell().appendChild(button);
        });
        if (!snapshots.length) {
          const cell = body.insertRow().insertCell();
          cell.colSpan = 6;
          cell.textContent = 'No snapshots in this date range. History appears after the next scheduled snapshot.';
        }
        offset = nextOffset;
        snapshotPage = { filter, hasNext: snapshots.length > 50 };
        info.textContent = `Page ${offset / 50 + 1}`;
        status.replaceChildren();
      } catch {
        if (request !== snapshotRequest) return;
        status.replaceChildren(document.createTextNode('Unable to load snapshots. '));
        const retry = document.createElement('button');
        retry.type = 'button';
        retry.className = 'btn btn-sm btn-secondary';
        retry.textContent = 'Retry';
        retry.addEventListener('click', () => loadSnapshots(nextOffset));
        status.appendChild(retry);
      } finally {
        if (request === snapshotRequest) {
          body.setAttribute('aria-busy', 'false');
          snapshotLoading = false;
          updateHistoryControls();
        }
      }
    }
    loadSnapshots(0);
  }

  function getChartOptions(currency) {
    const isDark = document.documentElement.getAttribute('data-theme') === 'dark';
    const gridColor = isDark ? 'rgba(255,255,255,0.1)' : 'rgba(0,0,0,0.05)';
    const textColor = isDark ? '#94a3b8' : '#64748b';

    return {
      responsive: true,
      maintainAspectRatio: false,
      interaction: {
        mode: 'index',
        intersect: false
      },
      plugins: {
        legend: { display: false },
        tooltip: {
          backgroundColor: isDark ? '#1e293b' : '#ffffff',
          titleColor: isDark ? '#f8fafc' : '#0f172a',
          bodyColor: isDark ? '#cbd5e1' : '#475569',
          borderColor: gridColor,
          borderWidth: 1,
          padding: 12,
          displayColors: false,
          callbacks: {
            label: function(context) {
              return `${context.dataset.label}: ${formatCurrency(context.raw, currency)}`;
            }
          }
        }
      },
      scales: {
        x: {
          grid: { display: false },
          ticks: { color: textColor, maxTicksLimit: 10 }
        },
        y: {
          grid: { color: gridColor },
          ticks: { 
            color: textColor,
            callback: function(value) {
              return formatCurrencyCompact(value, currency);
            }
          }
        }
      }
    };
  }

  function updateChartColors() {
    // Preserve data-dependent callbacks (especially the history currency).
    const colors = getChartOptions(state.displayCurrency);
    [state.charts.portfolio, state.charts.holding].forEach(chart => {
      if (!chart) return;
      ['backgroundColor', 'titleColor', 'bodyColor', 'borderColor'].forEach(key => {
        chart.options.plugins.tooltip[key] = colors.plugins.tooltip[key];
      });
      chart.options.scales.x.ticks.color = colors.scales.x.ticks.color;
      chart.options.scales.y.ticks.color = colors.scales.y.ticks.color;
      chart.options.scales.y.grid.color = colors.scales.y.grid.color;
      chart.update();
    });
    if (state.charts.allocation) {
      const chart = state.charts.allocation;
      chart.data.datasets.forEach(dataset => {
        dataset.borderColor = getComputedStyle(document.documentElement).getPropertyValue('--bg-card').trim();
      });
      chart.options.plugins.legend.labels.color = colors.scales.x.ticks.color;
      ['backgroundColor', 'titleColor', 'bodyColor', 'borderColor'].forEach(key => {
        chart.options.plugins.tooltip[key] = colors.plugins.tooltip[key];
      });
      chart.update();
    }
  }

  const tableOriginalOrders = new WeakMap();

  function sortTableRows(table, header, direction) {
    const body = table.tBodies[0];
    if (!body) return;
    const rows = [...body.rows];
    const original = (tableOriginalOrders.get(body) || rows).filter(row => row.parentNode === body);
    rows.forEach(row => { if (!original.includes(row)) original.push(row); });
    tableOriginalOrders.set(body, original);
    const numeric = ['quantity', 'current_price', 'est_price'].includes(header.dataset.sort);
    const value = row => {
      const cell = row.cells[header.cellIndex];
      if (!cell) return numeric ? null : '';
      if (!numeric) return cell.textContent.trim();
      const raw = cell.dataset.sortValue ?? cell.textContent.replace(/[₺$,\s]/g, '');
      return raw === '' || !Number.isFinite(Number(raw)) ? null : Number(raw);
    };
    const ordered = direction === 'none' ? original : rows.sort((left, right) => {
      if (numeric && (value(left) === null || value(right) === null)) {
        return value(left) === value(right) ? original.indexOf(left) - original.indexOf(right) :
          value(left) === null ? 1 : -1;
      }
      const comparison = numeric ? value(left) - value(right) :
        value(left).localeCompare(value(right), undefined, { numeric: true, sensitivity: 'base' });
      return comparison ? comparison * (direction === 'asc' ? 1 : -1) :
        original.indexOf(left) - original.indexOf(right);
    });
    body.append(...ordered);
  }

  // Table interactions
  function initTableInteractions() {
    // Sortable headers
    document.addEventListener('click', (e) => {
      const th = e.target.closest('th[data-sort]');
      if (th) {
        const table = th.closest('table');
        const sortKey = th.dataset.sort;
        const currentSort = table.dataset.sort;
        const currentDir = table.dataset.sortDir || 'asc';
        
        let newDir = 'asc';
        if (currentSort === sortKey) {
          newDir = currentDir === 'asc' ? 'desc' : 'none';
        }
        
        if (newDir === 'none') {
          delete table.dataset.sort;
          delete table.dataset.sortDir;
          th.classList.remove('sort-asc', 'sort-desc');
        } else {
          table.dataset.sort = sortKey;
          table.dataset.sortDir = newDir;
          th.classList.toggle('sort-asc', newDir === 'asc');
          th.classList.toggle('sort-desc', newDir === 'desc');
        }
        
        th.setAttribute('aria-sort', newDir === 'none' ? 'none' : newDir === 'asc' ? 'ascending' : 'descending');
        sortTableRows(table, th, newDir);

        // Update other headers
        table.querySelectorAll('th[data-sort]').forEach(other => {
          if (other !== th) {
            other.classList.remove('sort-asc', 'sort-desc');
            other.setAttribute('aria-sort', 'none');
          }
        });
      }
    });

    // Inline editing
    document.addEventListener('dblclick', (e) => {
      const td = e.target.closest('td.editable-cell');
      if (td && !td.querySelector('input, select, textarea')) {
        makeEditable(td);
      }
    });

    // Keyboard navigation for inline edit
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && e.target.closest('td.editable-cell') && !e.target.matches('input, select, textarea')) {
        const td = e.target.closest('td.editable-cell');
        makeEditable(td);
      }
    });
  }

  function initHoldingFilters() {
    const table = document.getElementById('holdings-table');
    if (!table) return;
    const filters = [...table.querySelectorAll('[data-filter]')];
    const reset = document.getElementById('reset-holding-filters');
    const mobileSearch = document.getElementById('mobile-holding-search');
    try {
      const saved = JSON.parse(sessionPreferences.getItem('portfolio-holding-filters') || '{}');
      filters.forEach(input => { input.value = saved?.[input.dataset.filter] || ''; });
    } catch { sessionPreferences.removeItem('portfolio-holding-filters'); }
    const apply = () => {
      let visible = 0;
      table.querySelectorAll('tbody .holding-row').forEach(row => {
        const matches = filters.every(input => {
          const value = row.querySelector(`[data-field="${input.dataset.filter}"]`)?.textContent.trim() || '';
          return input.tagName === 'SELECT' ? !input.value || value === input.value :
            value.toLocaleLowerCase().includes(input.value.trim().toLocaleLowerCase());
        });
        row.hidden = !matches;
        const card = document.querySelector(`.holding-card[data-id="${row.dataset.id}"]`);
        if (card) card.hidden = !matches;
        if (matches) visible++;
      });
      const active = filters.some(input => input.value);
      document.getElementById('holdings-filter-status').textContent = active ? `${visible} matching holdings` : '';
      reset.hidden = !active;
      mobileSearch.value = filters.find(input => input.dataset.filter === 'name').value;
      sessionPreferences.setItem('portfolio-holding-filters', JSON.stringify(Object.fromEntries(
        filters.map(input => [input.dataset.filter, input.value]))));
    };
    filters.forEach(input => input.addEventListener('input', apply));
    mobileSearch.addEventListener('input', () => {
      filters.find(input => input.dataset.filter === 'name').value = mobileSearch.value;
      apply();
    });
    reset.addEventListener('click', () => { filters.forEach(input => { input.value = ''; }); apply(); });
    apply();
    const toggle = document.getElementById('column-toggle');
    const menu = document.getElementById('column-menu');
    const headers = [...table.tHead.rows[0].cells];
    headers.slice(1, -1).forEach((header, offset) => {
      const index = offset + 1;
      const label = document.createElement('label');
      const checkbox = document.createElement('input');
      checkbox.type = 'checkbox';
      checkbox.checked = true;
      label.append(checkbox, document.createTextNode(header.textContent.trim()));
      menu.appendChild(label);
      checkbox.addEventListener('change', () => {
        [...table.rows].forEach(row => {
          if (row.cells.length === headers.length) row.cells[index].hidden = !checkbox.checked;
        });
      });
    });
    toggle.addEventListener('click', () => {
      const open = toggle.getAttribute('aria-expanded') !== 'true';
      toggle.setAttribute('aria-expanded', open);
      menu.setAttribute('aria-hidden', !open);
      if (open) positionMenu(menu, toggle);
    });
    document.addEventListener('click', event => {
      if (!toggle.contains(event.target) && !menu.contains(event.target)) {
        toggle.setAttribute('aria-expanded', 'false');
        menu.setAttribute('aria-hidden', 'true');
      }
    });
  }

  // A holding mutation may finish after the user has started another edit.
  // Coordinate inline, modal and HTMX writes before refreshing the page.
  const inlineEditors = new Map();
  const holdingHtmxRequests = new Map();
  let holdingWritesPending = 0;
  let holdingRefreshNeeded = false;
  let holdingReloadScheduled = false;
  let holdingWriteFailed = false;
  let holdingDeleteSucceeded = false;
  let holdingRefreshNotice;

  function initHoldingMutationTracking() {
    const mutation = config => config && /^(post|put|patch|delete)$/i.test(config.verb) &&
      /^\/api\/holdings(?:\/|$)/.test(config.path);
    document.body.addEventListener('htmx:beforeSend', event => {
      if (mutation(event.detail.requestConfig)) {
        holdingHtmxRequests.set(event.detail.xhr, event.detail.requestConfig);
      }
    });
    document.body.addEventListener('htmx:beforeSwap', event => {
      const config = event.detail.requestConfig || holdingHtmxRequests.get(event.detail.xhr);
      // A delayed delete swap could remove a newer editor inside its target row.
      // Keep the current view intact until the shared refresh can run safely.
      if (mutation(config) && config.verb.toLowerCase() === 'delete') event.detail.shouldSwap = false;
    });
    document.body.addEventListener('htmx:afterRequest', event => {
      const config = event.detail.requestConfig || holdingHtmxRequests.get(event.detail.xhr);
      holdingHtmxRequests.delete(event.detail.xhr);
      if (!mutation(config)) return;
      if (event.detail.successful) {
        holdingRefreshNeeded = true;
        if (config.verb.toLowerCase() === 'delete') holdingDeleteSucceeded = true;
      } else holdingWriteFailed = true;
      scheduleHoldingRefresh();
    });
  }

  function holdingReloadIsBlocked() {
    if (inlineEditors.size || holdingWritesPending || holdingHtmxRequests.size || holdingWriteFailed) return true;
    if (document.body.classList.contains('modal-open') ||
        document.querySelector('.modal-overlay, dialog[open], [aria-modal="true"], .inline-edit-input, .htmx-request, form[data-saving]')) return true;
    if (document.activeElement?.matches?.('input, select, textarea, [contenteditable="true"]')) return true;
    return [...document.querySelectorAll('form')].some(form => [...form.elements].some(control => {
      if (control.type === 'checkbox' || control.type === 'radio') return control.checked !== control.defaultChecked;
      if (control.type === 'file') return control.files.length > 0;
      if (control.tagName === 'INPUT' || control.tagName === 'TEXTAREA') return control.value !== control.defaultValue;
      if (control.tagName === 'SELECT') {
        const options = [...control.options];
        if (control.multiple) return options.some(option => option.selected !== option.defaultSelected);
        const defaultIndex = options.findIndex(option => option.defaultSelected);
        return control.selectedIndex !== (defaultIndex < 0 && options.length ? 0 : defaultIndex);
      }
      return false;
    }));
  }

  function scheduleHoldingRefresh() {
    if (!holdingRefreshNeeded || holdingReloadScheduled) return;
    holdingReloadScheduled = true;
    // Let a blur/click sequence open its next editor before checking for safety.
    setTimeout(() => {
      holdingReloadScheduled = false;
      if (!holdingRefreshNeeded || holdingWritesPending || holdingHtmxRequests.size) return;
      if (holdingReloadIsBlocked()) {
        const container = document.getElementById('toast-container');
        if (container) {
          if (!holdingRefreshNotice) {
            holdingRefreshNotice = document.createElement('div');
            holdingRefreshNotice.className = 'toast info';
            holdingRefreshNotice.setAttribute('role', 'status');
            container.appendChild(holdingRefreshNotice);
          }
          holdingRefreshNotice.textContent = `${holdingDeleteSucceeded ? 'Holding deleted.' : 'Changes saved.'} Finish editing, then refresh to update totals and the holdings list.`;
        }
        return;
      }
      holdingRefreshNeeded = false;
      window.location.reload();
    }, 0);
  }

  function makeEditable(td) {
    if (inlineEditors.has(td)) return;
    const field = td.dataset.field;
    const row = td.closest('tr');
    const holdingId = row.dataset.id;
    const currentValue = td.textContent.trim();

    let input;
    if (field === 'basket' || field === 'location') {
      input = document.createElement('input');
      input.type = 'text';
      const filter = document.querySelector(`select[data-filter="${field}"]`);
      let list = document.getElementById(`${field}-list`);
      if (!list) {
        list = document.createElement('datalist');
        list.id = `${field}-list`;
        [...(filter?.options || [])].filter(option => option.value).forEach(option => list.appendChild(option.cloneNode(true)));
        document.body.appendChild(list);
      }
      input.setAttribute('list', list.id);
    } else if (field === 'quantity' || field === 'est_price') {
      input = document.createElement('input');
      input.type = 'number';
      input.step = 'any';
      input.min = '0';
    } else {
      input = document.createElement('input');
      input.type = 'text';
    }

    const numericField = field === 'quantity' || field === 'est_price';
    const editValue = numericField ? (td.dataset.sortValue ?? currentValue.replace(/[₺$,\s]/g, '')) : (td.dataset.editValue ?? currentValue);
    input.setAttribute('aria-label', `Edit ${field.replace(/_/g, ' ')}`);
    input.value = editValue;
    input.className = 'inline-edit-input';
    input.style.cssText = 'width: 100%; padding: 4px 8px; border: 1px solid var(--border-focus); border-radius: 4px; background: var(--bg-primary); font-family: inherit; font-size: inherit;';

    td.innerHTML = '';
    td.appendChild(input);
    inlineEditors.set(td, input);
    input.focus();
    input.select();

    let finished = false;
    let reloadRequired = false;
    const finish = (value = currentValue) => {
      const returnFocus = document.activeElement === input;
      td.textContent = value;
      td.removeAttribute('aria-busy');
      inlineEditors.delete(td);
      if (returnFocus) td.focus();
    };
    const save = () => {
      if (finished || reloadRequired) return;
      if (!input.reportValidity()) return;
      finished = true;
      const newValue = input.value.trim();
      if (newValue !== String(editValue) && (newValue !== '' || !numericField)) {
        input.readOnly = true;
        input.setAttribute('aria-label', `Saving ${field.replace(/_/g, ' ')}…`);
        td.setAttribute('aria-busy', 'true');
        holdingWritesPending++;
        const url = `/api/holdings/${holdingId}`;
        const data = { [field]: field === 'quantity' || field === 'est_price' ? parseFloat(newValue) : newValue };
        
        fetch(url, {
          method: 'PATCH',
          headers: portfolioHeaders({ 'Content-Type': 'application/json', 'HX-Request': 'true' }),
          body: JSON.stringify(data)
        }).then(response => {
          if (!response.ok) {
            const error = new Error(portfolioErrorMessage(response, 'Unable to save this value. Please try again.'));
            error.reloadRequired = response.status === 409 || response.status === 428;
            throw error;
          }
          // Display the new value only after the server accepts the update.
          if (numericField) td.dataset.sortValue = newValue;
          else td.dataset.editValue = newValue;
          finish(formatDisplayValue(newValue, field));
          holdingRefreshNeeded = true;
        }).catch(error => {
          if (error.reloadRequired) {
            // A stale page cannot submit this draft safely. Leave it available
            // to copy or edit, and only discard it on an explicit Escape.
            reloadRequired = true;
            finished = false;
            input.readOnly = false;
            input.setAttribute('aria-label', `Edit ${field.replace(/_/g, ' ')} (reload required)`);
            td.removeAttribute('aria-busy');
          } else {
            finish();
          }
          // Do not immediately navigate away from a failed save's feedback if
          // another save in the same editing session succeeded.
          holdingWriteFailed = true;
          showToast(error.reloadRequired ? error.message : 'Unable to save this value. Please try again.', 'error');
        }).finally(() => {
          holdingWritesPending--;
          scheduleHoldingRefresh();
        });
      } else {
        finish();
        scheduleHoldingRefresh();
      }
    };

    input.addEventListener('blur', save);
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); save(); }
      if (e.key === 'Escape' && !finished) {
        e.preventDefault();
        finished = true;
        finish();
        scheduleHoldingRefresh();
      }
    });
  }

  function formatDisplayValue(value, field) {
    if (field === 'quantity' || field === 'est_price') {
      const num = parseFloat(value);
      return isNaN(num) ? '—' : num.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 8 });
    }
    return value || '—';
  }

  // Modal handling
  function initModalHandling() {
    const container = document.getElementById('modal-container');
    if (!container) return;
    let previousFocus;
    let modalRequest = 0;
    const modalRequests = new WeakMap();
    const formBaselines = new WeakMap();
    const captureBaseline = form => {
      if (form && !formBaselines.has(form)) formBaselines.set(form, Object.fromEntries(new FormData(form)));
    };
    document.body.addEventListener('htmx:beforeRequest', event => {
      const config = event.detail.requestConfig;
      if (event.detail.target === container && config?.verb?.toLowerCase() === 'get') {
        modalRequests.set(event.detail.xhr, ++modalRequest);
      }
    });
    document.body.addEventListener('htmx:beforeSwap', event => {
      const request = modalRequests.get(event.detail.xhr);
      if (request !== undefined && (request !== modalRequest || container.querySelector('.modal-overlay'))) {
        // Also cover queued repeat clicks: an already-open editor owns this target.
        event.detail.shouldSwap = false;
      }
    });
    const close = (modal = container.querySelector('.modal-overlay')) => {
      if (!modal || container.querySelector('.modal-overlay') !== modal) return;
      ++modalRequest; // A dismissed dialog must not be reopened by an older partial.
      const returnFocus = modal.contains(document.activeElement) || document.activeElement === document.body;
      container.replaceChildren();
      document.body.classList.remove('modal-open');
      if (returnFocus) previousFocus?.focus();
      scheduleHoldingRefresh();
    };
    document.body.addEventListener('htmx:afterSwap', (event) => {
      if (event.detail.target !== container || !container.querySelector('.modal-overlay')) return;
      captureBaseline(container.querySelector('[data-holding-form], [data-manual-price-form]'));
      previousFocus = document.activeElement;
      document.body.classList.add('modal-open');
      container.querySelector('input:not([type="hidden"]), button')?.focus();
    });
    document.addEventListener('keydown', (event) => {
      const modal = container.querySelector('.modal-overlay');
      if (!modal) return;
      if (event.key === 'Escape' && !event.target.matches('select')) close();
      if (event.key === 'Tab') {
        const controls = [...modal.querySelectorAll('button, input, select, textarea, a[href]')]
          .filter(control => !control.disabled && control.offsetParent !== null);
        const first = controls[0];
        const last = controls[controls.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last?.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first?.focus();
        }
      }
    });
    document.addEventListener('click', (event) => {
      if (event.target.closest('[data-close-modal]') || event.target.classList.contains('modal-overlay')) close();
      const clear = event.target.closest('[data-clear-manual-price]');
      if (clear) saveForm(clear.closest('form'), true);
    });
    document.addEventListener('submit', (event) => {
      const form = event.target;
      if (!form.matches('[data-holding-form], [data-manual-price-form]')) return;
      event.preventDefault();
      saveForm(form);
    });

    async function saveForm(form, clear = false) {
      if (form.dataset.saving || form.dataset.reloadRequired) return;
      const modal = form.closest('.modal-overlay');
      const manual = form.hasAttribute('data-manual-price-form');
      const id = Number(form.dataset.id);
      const data = Object.fromEntries(new FormData(form));
      const error = form.querySelector('.form-error');
      error.hidden = true;
      if (!manual && id) {
        const baseline = formBaselines.get(form);
        if (!baseline) {
          error.textContent = 'This form is out of date. Keep any unsaved edits, then reopen it before saving.';
          error.hidden = false;
          return;
        }
        // Never replace untouched fields with the older values rendered by a GET.
        Object.keys(data).forEach(field => {
          if (data[field] === baseline[field]) delete data[field];
        });
        if (!Object.keys(data).length) { close(modal); return; }
      }
      if (!manual && !id && data.initial_manual_price !== undefined) {
        const value = data.initial_manual_price;
        data.manual_price = { Float64: Number(value), Valid: value !== '' };
        delete data.initial_manual_price;
      }
      const numericFields = manual ? ['price'] : ['quantity', 'est_price'];
      for (const field of numericFields) if (field in data) data[field] = Number(data[field]);
      form.dataset.saving = 'true';
      const controls = [...form.elements].map(control => [control, control.disabled]);
      controls.forEach(([control]) => { control.disabled = true; });
      holdingWritesPending++;
      try {
        const headers = portfolioHeaders({ 'Content-Type': 'application/json' });
        if (!manual && !id) {
          if (!form.dataset.createKey) {
            const bytes = new Uint8Array(16);
            window.crypto.getRandomValues(bytes);
            form.dataset.createKey = Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('');
          }
          headers['Idempotency-Key'] = form.dataset.createKey;
        }
        const url = manual ? `/api/holdings/${id}/manual-price` : `/api/holdings${id ? `/${id}` : ''}`;
        const response = await fetch(url, {
          method: clear ? 'DELETE' : manual || id ? 'PATCH' : 'POST',
          headers,
          ...(clear ? {} : { body: JSON.stringify(data) })
        });
        if (!response.ok) {
          const body = await response.json().catch(() => ({}));
          if (response.status === 409 || response.status === 428) form.dataset.reloadRequired = 'true';
          throw new Error(portfolioErrorMessage(response, body.error || `Save failed: HTTP ${response.status}`));
        }
        holdingRefreshNeeded = true;
        // A dismissed form may have been replaced by an entirely new modal.
        // Only retire the submitted form while it still owns the current modal.
        if (modal && modal.contains(form)) close(modal);
      } catch (err) {
        holdingWriteFailed = true;
        error.textContent = err.message;
        error.hidden = false;
        if (!container.contains(form)) showToast(err.message, 'error');
      } finally {
        delete form.dataset.saving;
        controls.forEach(([control, disabled]) => { control.disabled = disabled; });
        holdingWritesPending--;
        scheduleHoldingRefresh();
      }
    }
  }

  // Pull to refresh (mobile)
  function initPullToRefresh() {
    let startY = 0;
    let currentY = 0;
    let pulling = false;
    let refreshing = false;
    const dirtyForms = new WeakSet();
    const trackEdit = event => {
      if (event.target.form) dirtyForms.add(event.target.form);
    };
    document.addEventListener('input', trackEdit);
    document.addEventListener('change', trackEdit);
    const editing = () => sourceRequestsPending || sourceDraftsPending() || holdingWritesPending || holdingHtmxRequests.size || inlineEditors.size || document.body.classList.contains('modal-open') ||
      document.querySelector('.modal-overlay, dialog[open], [aria-modal="true"], .inline-edit-input') ||
      [...document.querySelectorAll('form')].some(form => dirtyForms.has(form) ||
        [...form.elements].some(control => {
          if (control.type === 'checkbox' || control.type === 'radio') return control.checked !== control.defaultChecked;
          if (control.type === 'file') return control.files.length > 0;
          if (control.tagName === 'INPUT' || control.tagName === 'TEXTAREA') return control.value !== control.defaultValue;
          return false;
        }));
    const blockedTarget = target => {
      if (target.closest('input, select, textarea, button, a, [contenteditable], [role="button"]')) return true;
      for (let node = target; node && node !== document.body && node !== document.documentElement; node = node.parentElement) {
        if (node.scrollHeight > node.clientHeight && /(auto|scroll|overlay)/.test(getComputedStyle(node).overflowY)) return true;
      }
      return false;
    };
    const reset = () => {
      pulling = false;
      document.body.style.transform = '';
      document.body.style.transition = '';
    };
    document.addEventListener('touchstart', e => {
      reset();
      if (refreshing || priceRefreshActive || e.touches.length !== 1 || window.scrollY !== 0 || editing() || blockedTarget(e.target)) return;
      startY = currentY = e.touches[0].clientY;
      pulling = true;
    }, { passive: true });
    document.addEventListener('touchmove', e => {
      if (!pulling) return;
      if (e.touches.length !== 1 || window.scrollY !== 0 || editing() || blockedTarget(e.target)) {
        reset();
        return;
      }
      currentY = e.touches[0].clientY;
      const delta = currentY - startY;
      if (delta > 0) {
        e.preventDefault();
        document.body.style.transform = `translateY(${Math.min(delta, 100) * 0.5}px)`;
      }
    }, { passive: false });
    document.addEventListener('touchcancel', reset);
    document.addEventListener('touchend', () => {
      if (!pulling) return;
      const delta = currentY - startY;
      reset();
      if (delta <= 60 || refreshing || priceRefreshActive || editing()) return;
      refreshing = true;
      priceRefreshActive = true;
      showToast('Refreshing prices...', 'info');
      fetch('/api/sources/refresh', { method: 'POST', headers: portfolioHeaders() }).then(response => {
        if (!response.ok) throw new Error(portfolioErrorMessage(response, 'Price refresh failed'));
        // The user may have opened an editor while the refresh was in flight.
        if (!editing()) window.location.reload();
      }).catch(error => showToast(error.message, 'error'))
        .finally(() => { refreshing = false; priceRefreshActive = false; });
    });
  }

  // HTMX event handlers

  function onHtmxBeforeRequest(e) {
    // Show loading state
    if (e.detail.target.matches('.chart-container')) {
      e.detail.target.classList.add('loading');
    }
  }

  function onPortfolioUpdated() {
    // Refresh the selected views rather than resetting the user's tabs.
    const range = document.querySelector('.chart-tab[aria-selected="true"]')?.dataset.range || '1y';
    const allocation = document.querySelector('.alloc-tab[aria-selected="true"]')?.dataset.alloc || 'type';
    loadPortfolioChart(range);
    loadAllocationChart(allocation);
  }

  // Utility functions
  function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  function formatCurrency(value, currency) {
    if (value === null || value === undefined || isNaN(value)) return '—';
    const symbol = currency === 'USD' ? '$' : '₺';
    return `${symbol} ${value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
  }

  function formatCurrencyCompact(value, currency) {
    if (value >= 1e9) return formatCurrency(value / 1e9, currency).replace(/[₺$]/, '$&') + 'B';
    if (value >= 1e6) return formatCurrency(value / 1e6, currency).replace(/[₺$]/, '$&') + 'M';
    if (value >= 1e3) return formatCurrency(value / 1e3, currency).replace(/[₺$]/, '$&') + 'K';
    return formatCurrency(value, currency);
  }

  // Public API
  return {
    init,
    showToast,
    loadPortfolioChart,
    loadAllocationChart,
    formatCurrency,
    formatCurrencyCompact,
    state: () => state
  };
})();

// Auto-initialize when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => PortfolioApp.init());
} else {
  PortfolioApp.init();
}

// Export for global access
window.PortfolioApp = PortfolioApp;
// Keep the selected file local until the user confirms the restore.
(() => {
  let pendingFile;
  let restoring = false;
  document.addEventListener('change', event => {
    const input = event.target;
    if (input.id !== 'restore-file' || !input.files?.length || restoring) return;
    pendingFile = input.files[0];
    input.value = '';
    document.getElementById('restore-filename').textContent = pendingFile.name;
    document.getElementById('restore-error').hidden = true;
    document.getElementById('backup-status').textContent = '';
    document.getElementById('restore-review').hidden = false;
    document.getElementById('restore-cancel').focus();
  });
  document.addEventListener('click', async event => {
    const button = event.target.closest('#restore-cancel, #restore-confirm');
    if (!button || restoring) return;
    if (button.id === 'restore-cancel') {
      pendingFile = undefined;
      document.getElementById('restore-review').hidden = true;
      document.getElementById('restore-btn').focus();
      return;
    }
    if (!pendingFile) return;
    restoring = true;
    const controls = ['restore-btn', 'restore-cancel', 'restore-confirm'].map(id => document.getElementById(id));
    controls.forEach(control => { control.disabled = true; });
    const review = document.getElementById('restore-review');
    const error = document.getElementById('restore-error');
    review.setAttribute('aria-busy', 'true');
    error.hidden = true;
    button.textContent = 'Restoring…';
    const form = new FormData();
    form.append('file', pendingFile);
    try {
      const response = await fetch('/api/settings/restore', {
        method: 'POST', body: form, headers: portfolioHeaders({ 'X-Restore-Confirm': 'restore' })
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(portfolioErrorMessage(response, body.error || `Restore failed: HTTP ${response.status}`));
      localPreferences.removeItem('theme');
        sessionPreferences.removeItem('portfolio-holding-filters');
      sessionPreferences.setItem('portfolio-restore-success', 'Backup restored successfully.');
      window.location.assign('/settings');
    } catch (err) {
      error.textContent = err.message;
      error.hidden = false;
    } finally {
      restoring = false;
      controls.forEach(control => { control.disabled = false; });
      review.removeAttribute('aria-busy');
      button.textContent = 'Restore backup';
    }
  });
})();
