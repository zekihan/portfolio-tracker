(function () {
  'use strict';

  const offlineURL = '/static/offline.html';
  const generation = document.documentElement.dataset.portfolioGeneration;
  const currency = document.documentElement.dataset.currency || '';

  if (!('serviceWorker' in navigator)) return;
  navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(() => {});

  function text(node) {
    return node ? node.textContent.replace(/\s+/g, ' ').trim() : '';
  }

  function capture() {
    const dashboard = document.querySelector('.dashboard');
    const holdings = document.querySelector('.holdings-page');
    if (!generation) return;
    try {
      const minimum = localStorage.getItem(offlineMinGenerationKey);
      if (minimum !== null && BigInt(generation) < BigInt(minimum)) return;
      let saved;
      try { saved = JSON.parse(localStorage.getItem(offlineSnapshotKey)); } catch (_) { saved = null; }
      if (saved && saved.generation !== generation) {
        // A tab opened before a restore must not overwrite a newer saved view.
        if (/^\d+$/.test(saved.generation) && /^\d+$/.test(generation) &&
            BigInt(saved.generation) > BigInt(generation)) return;
        saved = null;
      }
      if (!saved) saved = { generation: generation };
      if (!dashboard && !holdings) {
        localStorage.setItem(offlineSnapshotKey, JSON.stringify(saved));
        return;
      }
      const section = { capturedAt: new Date().toISOString(), currency: currency };
      if (dashboard) {
        const current = dashboard.querySelector('#current-value-try');
        const estimated = dashboard.querySelector('#estimated-value-try');
        const alert = dashboard.querySelector('[role="alert"]');
        section.notice = text(alert && alert.querySelector('h1, h2'));
        section.detail = text(alert && alert.querySelector('p'));
        section.currentLabel = text(current && current.closest('article').querySelector('h3')) || 'Current Value';
        section.current = text(current) || 'Unavailable';
        section.estimatedLabel = text(estimated && estimated.closest('article').querySelector('h3')) || 'Estimated Value';
        section.estimated = text(estimated) || 'Unavailable';
        section.calculated = text(dashboard.querySelector('#last-updated'));
        saved.overview = section;
      } else {
        const table = holdings.querySelector('#holdings-tbody');
        const alert = holdings.querySelector('[role="alert"]');
        section.notice = text(alert && alert.querySelector('h2'));
        section.detail = text(alert && alert.querySelector('p'));
        section.available = Boolean(table);
        section.rows = table ? Array.from(table.querySelectorAll('tr.holding-row')).map(row => {
          const cells = row.querySelectorAll('td');
          return {
            name: text(row.querySelector('[data-field="name"]')),
            ticker: text(row.querySelector('[data-field="ticker"]')),
            quantity: text(row.querySelector('[data-field="quantity"]')),
            price: text(row.querySelector('[data-field="price"]')),
            total: text(cells[6]),
            estimated: text(cells[7])
          };
        }) : [];
        saved.holdings = section;
      }
      localStorage.setItem(offlineSnapshotKey, JSON.stringify(saved));
    } catch (_) { /* Storage is optional. */ }
  }

  const editNotice = document.getElementById('offline-edit-notice');

  function goOffline() {
    if (!navigator.serviceWorker.controller) return;
    if (window.PortfolioApp?.hasPendingEdits()) {
      editNotice.hidden = false;
      return;
    }
    location.replace(offlineURL);
  }

  let checking = false;
  async function checkConnection() {
    if (checking || document.visibilityState !== 'visible' || !navigator.serviceWorker.controller) return;
    checking = true;
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 5000);
    try {
      const response = await fetch('/api/settings', { cache: 'no-store', redirect: 'manual', signal: controller.signal });
      editNotice.hidden = true;
      if (response.status === 401 || response.status === 403 || response.type === 'opaqueredirect') {
        localPreferences.removeItem(offlineSnapshotKey);
      } else if (response.ok) {
        const currentGeneration = response.headers.get('X-Portfolio-Generation');
        if (currentGeneration !== null && /^\d+$/.test(currentGeneration)) {
          discardOfflineSnapshotBefore(BigInt(currentGeneration));
        }
      }
    } catch (_) {
      if (!controller.signal.aborted) goOffline();
    } finally {
      clearTimeout(timeout);
      checking = false;
    }
  }

  window.addEventListener('offline', goOffline);
  window.addEventListener('focus', checkConnection);
  window.addEventListener('online', checkConnection);
  document.addEventListener('visibilitychange', checkConnection);
  setInterval(checkConnection, 30000);
  if (!navigator.onLine) goOffline();
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', capture);
  else capture();
})();
