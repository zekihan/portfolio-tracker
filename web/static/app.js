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

  // Chart color palette
  const CHART_COLORS = [
    '#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#06b6d4',
    '#f97316', '#ec4899', '#14b8a6', '#6366f1', '#84cc16', '#eab308'
  ];

  // Initialize app
  function init(options = {}) {
    state.displayCurrency = options.displayCurrency || document.documentElement.dataset.currency || 'TRY';
    state.theme = localStorage.getItem('theme') || 'system';
    
    applyTheme(state.theme);
    initThemeToggle();
    initCurrencySwitcher();
    initMobileMenu();
    initDropdowns();
    initToasts();
    initCharts();
    initTableInteractions();
    initModalHandling();
    initPullToRefresh();
    
    // Listen for HTMX events
    document.body.addEventListener('htmx:afterSwap', onHtmxAfterSwap);
    document.body.addEventListener('htmx:beforeRequest', onHtmxBeforeRequest);
    document.body.addEventListener('portfolio-updated', onPortfolioUpdated);
    
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

  async function loadCurrencyOptions() {
    const menu = document.getElementById('currency-menu');
    if (!menu) return;

    try {
      const response = await fetch('/api/settings');
      const settings = await response.json();
      
      menu.innerHTML = `
        <button class="dropdown-item ${settings.display_currency === 'TRY' ? 'active' : ''}" 
                hx-patch="/api/settings/currency" 
                hx-vals='{"currency": "TRY"}' 
                hx-target="#toast-container" 
                hx-swap="beforeend"
                hx-on::after-request="closeDropdown()">
          Turkish Lira (TRY)
        </button>
        <button class="dropdown-item ${settings.display_currency === 'USD' ? 'active' : ''}" 
                hx-patch="/api/settings/currency" 
                hx-vals='{"currency": "USD"}' 
                hx-target="#toast-container" 
                hx-swap="beforeend"
                hx-on::after-request="closeDropdown()">
          US Dollar (USD)
        </button>
      `;
    } catch (e) {
      console.error('Failed to load currency options:', e);
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
      nav.style.display = expanded ? 'none' : 'flex';
    });
  }

  // Dropdowns
  function initDropdowns() {
    document.addEventListener('click', (e) => {
      const dropdown = e.target.closest('.dropdown');
      if (dropdown) {
        const toggle = dropdown.querySelector('.dropdown-toggle');
        const menu = dropdown.querySelector('.dropdown-menu');
        if (toggle && menu) {
          const expanded = toggle.getAttribute('aria-expanded') === 'true';
          toggle.setAttribute('aria-expanded', !expanded);
          menu.setAttribute('aria-hidden', expanded);
        }
      } else {
        // Close all dropdowns
        document.querySelectorAll('.dropdown-toggle[aria-expanded="true"]').forEach(toggle => {
          toggle.setAttribute('aria-expanded', 'false');
          const menu = toggle.closest('.dropdown')?.querySelector('.dropdown-menu');
          if (menu) menu.setAttribute('aria-hidden', 'true');
        });
      }
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
    const daysMap = { '1y': 365, '6m': 180, '3m': 90, '1m': 30 };
    const days = daysMap[range] || 365;
    
    fetch(`/api/history/chart?days=${days}`)
      .then(r => r.json())
      .then(data => renderPortfolioChart(data, range))
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

    const isUSD = state.displayCurrency === 'USD';
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
    const params = new URLSearchParams({ type, currency: state.displayCurrency });
    fetch(`/api/dashboard/allocation?${params}`)
      .then(r => r.json())
      .then(data => renderAllocationChart(data, type))
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
        
        // Update other headers
        table.querySelectorAll('th[data-sort]').forEach(other => {
          if (other !== th) {
            other.classList.remove('sort-asc', 'sort-desc');
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

  function makeEditable(td) {
    const field = td.dataset.field;
    const row = td.closest('tr');
    const holdingId = row.dataset.id;
    const currentValue = td.textContent.trim();

    let input;
    if (field === 'basket' || field === 'location') {
      input = document.createElement('input');
      input.type = 'text';
      input.list = `${field}-list`;
      // Would need to fetch existing values for datalist
    } else if (field === 'quantity' || field === 'est_price') {
      input = document.createElement('input');
      input.type = 'number';
      input.step = '0.01';
      input.min = '0';
    } else {
      input = document.createElement('input');
      input.type = 'text';
    }

    input.value = currentValue === '—' ? '' : currentValue.replace(/[₺$,\s]/g, '');
    input.className = 'inline-edit-input';
    input.style.cssText = 'width: 100%; padding: 4px 8px; border: 1px solid var(--border-focus); border-radius: 4px; background: var(--bg-primary); font-family: inherit; font-size: inherit;';

    td.innerHTML = '';
    td.appendChild(input);
    input.focus();
    input.select();

    const save = () => {
      const newValue = input.value.trim();
      if (newValue !== currentValue && newValue !== '') {
        // Send update via HTMX
        const url = `/api/holdings/${holdingId}`;
        const data = { [field]: field === 'quantity' || field === 'est_price' ? parseFloat(newValue) : newValue };
        
        fetch(url, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json', 'HX-Request': 'true' },
          body: JSON.stringify(data)
        }).then(() => {
          // Reload row or update
          td.textContent = formatDisplayValue(newValue, field);
        }).catch(() => {
          td.textContent = currentValue;
        });
      } else {
        td.textContent = currentValue;
      }
    };

    input.addEventListener('blur', save);
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') save();
      if (e.key === 'Escape') td.textContent = currentValue;
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
    // Close modal on escape
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        const modal = document.querySelector('.modal-overlay');
        if (modal) {
          const closeBtn = modal.querySelector('.modal-close');
          if (closeBtn) closeBtn.click();
        }
      }
    });

    // Close modal on overlay click
    document.addEventListener('click', (e) => {
      if (e.target.classList.contains('modal-overlay')) {
        const closeBtn = e.target.querySelector('.modal-close');
        if (closeBtn) closeBtn.click();
      }
    });
  }

  // Pull to refresh (mobile)
  function initPullToRefresh() {
    let startY = 0;
    let currentY = 0;
    let pulling = false;

    document.addEventListener('touchstart', (e) => {
      if (window.scrollY === 0) {
        startY = e.touches[0].clientY;
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
        document.body.dispatchEvent(new CustomEvent('portfolio-updated'));
        showToast('Refreshing prices...', 'info');
      }
    });
  }

  // HTMX event handlers
  function onHtmxAfterSwap(e) {
    // Dropdown clicks are delegated on document; no rebinding is needed.
    // Re-initialize element-bound components in swapped content.
    initTableInteractions();
    
    // Trigger chart updates if needed
    if (e.detail.target.id === 'portfolio-chart') {
      // Chart will be re-rendered by the partial
    }
  }

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