// Send HTMX request bodies as JSON: the API handlers only accept application/json.
if (window.htmx) {
  htmx.defineExtension('json-body', {
    onEvent: (name, evt) => {
      if (name === 'htmx:configRequest') {
        evt.detail.headers['Content-Type'] = 'application/json';
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

  // Chart color palette
  const CHART_COLORS = [
    '#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#06b6d4',
    '#f97316', '#ec4899', '#14b8a6', '#6366f1', '#84cc16', '#eab308'
  ];

  // Initialize app
  function init(options = {}) {
    state.displayCurrency = options.displayCurrency || document.documentElement.dataset.currency || 'TRY';
    state.theme = localStorage.getItem('theme') || document.documentElement.dataset.themePreference || 'system';
    
    applyTheme(state.theme);
    initThemeToggle();
    initCurrencySwitcher();
    initDisplaySettings();
    initSources();
    initProviderHealth();
    initMobileMenu();
    initDropdowns();
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
    document.body.addEventListener('htmx:afterRequest', event => {
      if (event.detail.successful && event.detail.requestConfig?.verb === 'delete' &&
          event.detail.requestConfig.path.startsWith('/api/holdings/')) window.location.reload();
    });
    document.body.addEventListener('htmx:responseError', () => showToast('The request failed. Please try again.', 'error'));
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
      localStorage.setItem('theme', next);
      applyTheme(next);
      const themeSelect = document.getElementById('dark-mode');
      if (themeSelect) themeSelect.value = next;
      updateToggle();
    });

    updateToggle();

    // Listen for system theme changes
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', (e) => {
      if (localStorage.getItem('theme') === 'system' || !localStorage.getItem('theme')) {
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
      backupStatus.textContent = sessionStorage.getItem('portfolio-restore-success') || '';
      sessionStorage.removeItem('portfolio-restore-success');
    }
    const clear = document.getElementById('clear-all-data');
    clear?.addEventListener('click', async () => {
      if (!await confirmAction('This will delete all holdings, snapshots, and settings. Download a backup first to keep the current data.', 'Clear all data')) return;
      clear.disabled = true;
      try {
        const response = await fetch('/api/settings/clear-all', { method: 'DELETE' });
        if (!response.ok) throw new Error('Unable to clear data');
        localStorage.removeItem('theme');
        sessionStorage.removeItem('portfolio-holding-filters');
        window.location.assign('/settings');
      } catch (error) {
        clear.disabled = false;
        showToast(error.message, 'error');
      }
    });
    const currency = document.getElementById('display-currency');
    currency?.addEventListener('change', async () => {
      currency.disabled = true;
      try {
        const response = await fetch('/api/settings/currency', {
          method: 'PATCH', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ currency: currency.value })
        });
        if (!response.ok) throw new Error('Unable to save currency');
        const url = new URL(window.location.href);
        url.searchParams.set('currency', currency.value);
        window.location.assign(url.href);
      } catch (error) {
        currency.value = state.displayCurrency;
        currency.disabled = false;
        showToast(error.message, 'error');
      }
    });
    const theme = document.getElementById('dark-mode');
    if (theme) theme.value = localStorage.getItem('theme') || document.documentElement.dataset.themePreference || 'system';
    theme?.addEventListener('change', async () => {
      theme.disabled = true;
      try {
        const response = await fetch('/api/settings/dark-mode', {
          method: 'PATCH', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ mode: theme.value })
        });
        if (!response.ok) throw new Error('Unable to save theme');
        localStorage.setItem('theme', theme.value);
        applyTheme(theme.value);
        document.getElementById('themeToggle')?.setAttribute('aria-pressed', document.documentElement.dataset.theme === 'dark');
        showToast('Theme saved', 'success');
      } catch (error) {
        theme.value = localStorage.getItem('theme') || 'system';
        showToast(error.message, 'error');
      } finally { theme.disabled = false; }
    });
  }

  function initSources() {
    if (!document.querySelector('.sources-page')) return;
    async function request(url, method, data) {
      const response = await fetch(url, {
        method, headers: { 'Content-Type': 'application/json' },
        ...(data ? { body: JSON.stringify(data) } : {})
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok || body.success === false) throw new Error(body.error || 'Request failed');
      return body;
    }
    document.querySelector('.sources-page').addEventListener('click', async (event) => {
      const button = event.target.closest('[data-test-provider], [data-test-mapping], [data-save-mapping], #refresh-prices');
      if (!button) return;
      button.disabled = true;
      const output = button.closest('tr')?.querySelector('.source-test-result');
      if (output) {
        output.textContent = 'Checking…';
        output.classList.remove('text-error');
      }
      try {
        if (button.id === 'refresh-prices') {
          await request('/api/sources/refresh', 'POST');
          window.location.reload();
          return;
        }
        const row = button.closest('tr');
        const provider = button.dataset.testProvider || row.querySelector('.provider-select').value;
        const instrumentId = row.querySelector('.instrument-input, .provider-test-instrument').value.trim();
        if (button.hasAttribute('data-save-mapping')) {
          await request(`/api/holdings/${row.dataset.holdingId}/provider`, 'PUT', { provider, instrumentId });
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
      } finally { button.disabled = false; }
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
      button.addEventListener('click', () => {
        const currency = button.dataset.currency;
        button.disabled = true;
        fetch('/api/settings/currency', {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ currency })
        }).then(response => {
          if (!response.ok) throw new Error(`Currency update failed: HTTP ${response.status}`);
          closeDropdown();
          const url = new URL(window.location.href);
          url.searchParams.set('currency', currency);
          window.location.assign(url.href);
        }).catch(() => {
          button.disabled = false;
          showToast('Failed to update display currency', 'error');
        });
      });
    });
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
    document.addEventListener('keydown', event => {
      if (event.key === 'Escape') closeMenus();
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

  function loadPortfolioChart(range) {
    if (!document.getElementById('portfolio-chart')) return;
    const daysMap = { '1y': 365, '6m': 180, '3m': 90, '1m': 30 };
    const days = document.getElementById('date-range')?.value || daysMap[range] || 365;
    
    const request = ++portfolioRequest;
    fetch(`/api/history/chart?days=${days}`)
      .then(response => {
        if (!response.ok) throw new Error('Unable to load portfolio history');
        return response.json();
      })
      .then(data => { if (request === portfolioRequest) renderPortfolioChart(data, range); })
      .catch(e => console.error('Failed to load portfolio chart:', e));
  }

  function renderPortfolioChart(data, range) {
    const ctx = document.getElementById('portfolio-chart');
    if (!ctx) return;

    if (state.charts.portfolio) {
      state.charts.portfolio.destroy();
    }

    const labels = data.map(d => new Date(d.timestamp).toLocaleDateString(undefined, { month: 'short', day: 'numeric' }));
    const valuesTry = data.map(d => d.value_try);
    const valuesUsd = data.map(d => d.value_usd);

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
          spanGaps: true
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
    fetch(`/api/dashboard/allocation?${params}`)
      .then(response => {
        if (!response.ok) throw new Error('Unable to load allocation');
        return response.json();
      })
      .then(data => { if (request === allocationRequest) renderAllocationChart(data, type); })
      .catch(e => console.error('Failed to load allocation chart:', e));
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
      const request = ++holdingRequest;
      state.charts.holding?.destroy();
      state.charts.holding = null;
      if (!select.value) return;
      try {
        const response = await fetch(`/api/history/holding/${select.value}?days=${range.value}`);
        if (!response.ok) throw new Error('Unable to load holding history');
        const data = await response.json();
        if (request !== holdingRequest) return;
        state.charts.holding = new Chart(document.getElementById('holding-chart'), {
          type: 'line',
          data: {
            labels: data.map(point => new Date(point.timestamp).toLocaleDateString()),
            datasets: [{ label: `Holding value (${currency.value})`,
              data: data.map(point => currency.value === 'USD' ? point.value_usd : point.value_try),
              borderColor: CHART_COLORS[0], tension: 0.1 }]
          },
          options: getChartOptions(currency.value)
        });
      } catch (error) { showToast(error.message, 'error'); }
    }

    const first = document.getElementById('first-page');
    const previous = document.getElementById('prev-page');
    const next = document.getElementById('next-page');
    first.addEventListener('click', () => loadSnapshots(0));
    previous.addEventListener('click', () => loadSnapshots(Math.max(0, offset - 50)));
    next.addEventListener('click', () => loadSnapshots(offset + 50));
    async function loadSnapshots(nextOffset) {
      const request = ++snapshotRequest;
      [first, previous, next].forEach(button => { button.disabled = true; });
      try {
        const response = await fetch(`/api/history/snapshots?limit=51&offset=${nextOffset}&days=${range.value}`);
        if (!response.ok) throw new Error('Unable to load snapshots');
        const snapshots = (await response.json()) || [];
        if (request !== snapshotRequest) return;
        document.getElementById('snapshot-value-heading').textContent = `Value (${currency.value})`;
        const body = document.getElementById('snapshots-tbody');
        body.replaceChildren();
        snapshots.slice(0, 50).forEach(snapshot => {
          const row = body.insertRow();
          [new Date(snapshot.timestamp).toISOString().replace('T', ' ').slice(0, 19),
            formatCurrency(currency.value === 'USD' ? snapshot.total_value_usd : snapshot.total_value_try, currency.value), snapshot.usd_try_rate,
            snapshot.stale_count, snapshot.failed_count, ''].forEach(value => {
            row.insertCell().textContent = value;
          });
        });
        if (!snapshots.length) {
          const cell = body.insertRow().insertCell();
          cell.colSpan = 6;
          cell.textContent = 'No snapshots in this date range. History appears after the next scheduled snapshot.';
        }
        offset = nextOffset;
        document.getElementById('page-info').textContent = `Page ${offset / 50 + 1}`;
        next.disabled = snapshots.length <= 50;
      } catch (error) {
        if (request !== snapshotRequest) return;
        showToast(error.message, 'error');
        next.disabled = false;
      } finally {
        if (request === snapshotRequest) first.disabled = previous.disabled = offset === 0;
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
    if (state.charts.portfolio) {
      state.charts.portfolio.options = getChartOptions(state.displayCurrency);
      state.charts.portfolio.update();
    }
    if (state.charts.allocation) {
      state.charts.allocation.update();
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
      if (!numeric) return cell.textContent.trim();
      return Number(cell.dataset.sortValue ?? cell.textContent.replace(/[₺$,\s]/g, '')) || 0;
    };
    const ordered = direction === 'none' ? original : rows.sort((left, right) => {
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
      const saved = JSON.parse(sessionStorage.getItem('portfolio-holding-filters') || '{}');
      filters.forEach(input => { input.value = saved?.[input.dataset.filter] || ''; });
    } catch { sessionStorage.removeItem('portfolio-holding-filters'); }
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
      sessionStorage.setItem('portfolio-holding-filters', JSON.stringify(Object.fromEntries(
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

  function makeEditable(td) {
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
    input.value = editValue;
    input.className = 'inline-edit-input';
    input.style.cssText = 'width: 100%; padding: 4px 8px; border: 1px solid var(--border-focus); border-radius: 4px; background: var(--bg-primary); font-family: inherit; font-size: inherit;';

    td.innerHTML = '';
    td.appendChild(input);
    input.focus();
    input.select();

    let finished = false;
    const save = () => {
      if (finished) return;
      if (!input.reportValidity()) return;
      finished = true;
      const newValue = input.value.trim();
      if (newValue !== String(editValue) && (newValue !== '' || !numericField)) {
        // Send update via HTMX
        const url = `/api/holdings/${holdingId}`;
        const data = { [field]: field === 'quantity' || field === 'est_price' ? parseFloat(newValue) : newValue };
        
        fetch(url, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json', 'HX-Request': 'true' },
          body: JSON.stringify(data)
        }).then(response => {
          if (!response.ok) throw new Error(`Holding update failed: HTTP ${response.status}`);
          // Display the new value only after the server accepts the update.
          if (numericField) td.dataset.sortValue = newValue;
          else td.dataset.editValue = newValue;
          td.textContent = formatDisplayValue(newValue, field);
          window.location.reload();
        }).catch(() => {
          td.textContent = currentValue;
          showToast('Unable to save this value. Please try again.', 'error');
        });
      } else {
        td.textContent = currentValue;
      }
    };

    input.addEventListener('blur', save);
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') save();
      if (e.key === 'Escape') {
        finished = true;
        td.textContent = currentValue;
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
    let previousFocus;
    const close = () => {
      container.replaceChildren();
      document.body.classList.remove('modal-open');
      previousFocus?.focus();
    };
    document.body.addEventListener('htmx:afterSwap', (event) => {
      if (event.detail.target !== container || !container.querySelector('.modal-overlay')) return;
      previousFocus = document.activeElement;
      document.body.classList.add('modal-open');
      container.querySelector('input:not([type="hidden"]), button')?.focus();
    });
    document.addEventListener('keydown', (event) => {
      const modal = container.querySelector('.modal-overlay');
      if (!modal) return;
      if (event.key === 'Escape') close();
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
      if (form.dataset.saving) return;
      const manual = form.hasAttribute('data-manual-price-form');
      const id = Number(form.dataset.id);
      const data = Object.fromEntries(new FormData(form));
      if (!manual && !id && data.initial_manual_price !== undefined) {
        const value = data.initial_manual_price;
        data.manual_price = { Float64: Number(value), Valid: value !== '' };
        delete data.initial_manual_price;
      }
      const numericFields = manual ? ['price'] : ['quantity', 'est_price'];
      for (const field of numericFields) data[field] = Number(data[field]);
      const error = form.querySelector('.form-error');
      error.hidden = true;
      form.dataset.saving = 'true';
      form.querySelectorAll('button').forEach(button => { button.disabled = true; });
      try {
        const url = manual ? `/api/holdings/${id}/manual-price` : `/api/holdings${id ? `/${id}` : ''}`;
        const response = await fetch(url, {
          method: clear ? 'DELETE' : manual ? 'PATCH' : id ? 'PUT' : 'POST',
          headers: { 'Content-Type': 'application/json' },
          ...(clear ? {} : { body: JSON.stringify(data) })
        });
        if (!response.ok) {
          const body = await response.json().catch(() => ({}));
          throw new Error(body.error || `Save failed: HTTP ${response.status}`);
        }
        window.location.reload();
      } catch (err) {
        error.textContent = err.message;
        error.hidden = false;
        delete form.dataset.saving;
        form.querySelectorAll('button').forEach(button => { button.disabled = false; });
      }
    }
  }

  // Pull to refresh (mobile)
  function initPullToRefresh() {
    let startY = 0;
    let currentY = 0;
    let pulling = false;

    document.addEventListener('touchstart', (e) => {
      if (window.scrollY === 0) {
        startY = e.touches[0].clientY;
        currentY = startY;
        pulling = true;
      }
    }, { passive: true });

    document.addEventListener('touchmove', (e) => {
      if (!pulling) return;
      currentY = e.touches[0].clientY;
      const delta = currentY - startY;
      
      if (delta > 0 && delta < 100) {
        e.preventDefault();
        document.body.style.transform = `translateY(${delta * 0.5}px)`;
      }
    }, { passive: false });

    document.addEventListener('touchcancel', () => {
      if (!pulling) return;
      pulling = false;
      document.body.style.transform = 'translateY(0)';
      document.body.style.transition = '';
    });

    document.addEventListener('touchend', () => {
      if (!pulling) return;
      pulling = false;
      const delta = currentY - startY;
      
      document.body.style.transition = 'transform 0.3s ease';
      document.body.style.transform = 'translateY(0)';
      
      setTimeout(() => {
        document.body.style.transition = '';
      }, 300);

      if (delta > 60) {
        // Trigger refresh
        showToast('Refreshing prices...', 'info');
        fetch('/api/sources/refresh', { method: 'POST' }).then(response => {
          if (!response.ok) throw new Error('Price refresh failed');
          window.location.reload();
        }).catch(error => showToast(error.message, 'error'));
      }
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
        method: 'POST', body: form, headers: { 'X-Restore-Confirm': 'restore' }
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `Restore failed: HTTP ${response.status}`);
      localStorage.removeItem('theme');
        sessionStorage.removeItem('portfolio-holding-filters');
      sessionStorage.setItem('portfolio-restore-success', 'Backup restored successfully.');
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
