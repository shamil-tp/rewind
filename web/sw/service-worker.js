// SPDX-License-Identifier: Apache-2.0
//
// Rewind service worker (template). The production build (sw/plugin.ts)
// writes it to dist/sw.js and fills in MANIFEST: a version derived from the
// content of every cached file, and the list of URLs to cache.
//
// Policy, deliberately conservative:
//   - Only the versioned application shell is cached: the hashed files in
//     /assets/, the offline page, the manifest and the icons. They are cached
//     once, at install time; nothing is ever added to the cache at runtime.
//   - /api/ requests (authentication, library data, audio streams), requests
//     with a Range header, non-GET requests and other origins are never
//     intercepted: the browser handles them exactly as without a service
//     worker. Music and private data are never cached.
//   - Page navigations always go to the network, so the server stays the
//     authority. Only when the network fails is the offline page shown.
//   - When a new version activates, caches from older versions are deleted.

'use strict'

const MANIFEST = /*__REWIND_MANIFEST__*/ { version: 'dev', urls: [] }

const CACHE_PREFIX = 'rewind-shell-'
const CACHE_NAME = CACHE_PREFIX + MANIFEST.version
const OFFLINE_URL = '/offline.html'
const PRECACHED = new Set(MANIFEST.urls)

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(CACHE_NAME)
      // cache: 'reload' bypasses the HTTP cache so the stored copies match
      // this version exactly.
      .then((cache) => cache.addAll(MANIFEST.urls.map((url) => new Request(new URL(url, self.location.href), { cache: 'reload' }))))
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((names) => Promise.all(names.filter((n) => n.startsWith(CACHE_PREFIX) && n !== CACHE_NAME).map((n) => caches.delete(n))))
      .then(() => self.clients.claim()),
  )
})

/**
 * Decides how a request is handled: 'network' (not intercepted at all),
 * 'navigate' (network, offline page on failure) or 'shell' (cached copy of a
 * versioned shell file, network if missing).
 */
function classify(request) {
  if (request.method !== 'GET') return 'network'
  const url = new URL(request.url)
  if (url.origin !== self.location.origin) return 'network'
  if (url.pathname === '/api' || url.pathname.startsWith('/api/')) return 'network'
  if (request.headers.has('range')) return 'network'
  if (request.mode === 'navigate') return 'navigate'
  if (url.search === '' && PRECACHED.has(url.pathname)) return 'shell'
  return 'network'
}

async function offlinePage() {
  const cache = await caches.open(CACHE_NAME)
  const page = await cache.match(OFFLINE_URL)
  return (
    page ||
    new Response('Rewind cannot reach its server. Check your connection and try again.', {
      status: 503,
      headers: { 'Content-Type': 'text/plain; charset=utf-8' },
    })
  )
}

async function shellFile(request) {
  const cache = await caches.open(CACHE_NAME)
  const hit = await cache.match(request)
  return hit || fetch(request)
}

self.addEventListener('fetch', (event) => {
  switch (classify(event.request)) {
    case 'navigate':
      event.respondWith(fetch(event.request).catch(() => offlinePage()))
      break
    case 'shell':
      event.respondWith(shellFile(event.request))
      break
    default:
    // Not intercepted: no respondWith, so the browser fetches normally.
  }
})
