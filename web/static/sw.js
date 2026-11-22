const CACHE_PREFIX = 'portfolio-tracker-offline-';
// Bump this version when changing the offline document so installed apps refresh it.
const CACHE_NAME = `${CACHE_PREFIX}v2`;
const OFFLINE_URL = '/static/offline.html';
const OFFLINE_MARKER = '<meta name="application-name" content="Portfolio Tracker Offline">';
const PAGE_PATHS = new Set(['/', '/holdings', '/history', '/sources', '/settings']);

self.addEventListener('install', event => {
  event.waitUntil((async () => {
    const response = await fetch(OFFLINE_URL, { cache: 'no-store', redirect: 'error' });
    if (!response.ok || response.redirected ||
        !response.headers.get('content-type')?.toLowerCase().includes('text/html') ||
        !(await response.clone().text()).includes(OFFLINE_MARKER)) {
      throw new Error('Invalid offline page');
    }
    const cache = await caches.open(CACHE_NAME);
    await cache.put(OFFLINE_URL, response);
  })());
});

self.addEventListener('activate', event => {
  event.waitUntil((async () => {
    const names = await caches.keys();
    await Promise.all(names.filter(name => name.startsWith(CACHE_PREFIX) && name !== CACHE_NAME)
      .map(name => caches.delete(name)));
    await self.clients.claim();
  })());
});

async function offlinePage() {
  const cache = await caches.open(CACHE_NAME);
  return (await cache.match(OFFLINE_URL)) || new Response('Offline', {
    status: 503, headers: { 'Content-Type': 'text/plain; charset=utf-8' }
  });
}

async function networkFirst(request) {
  try {
    return await fetch(request);
  } catch (_) {
    return offlinePage();
  }
}

self.addEventListener('fetch', event => {
  const request = event.request;
  if (request.method !== 'GET' || request.mode !== 'navigate') return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (url.pathname === OFFLINE_URL || PAGE_PATHS.has(url.pathname.replace(/\/$/, '') || '/')) {
    event.respondWith(networkFirst(request));
  }
});
