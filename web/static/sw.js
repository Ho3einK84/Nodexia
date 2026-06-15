/* Nodexia service worker.
 *
 * Served from the site root (GET /sw.js) so its default scope is the whole
 * origin. Strategy — see docs/pwa.md for the full rationale:
 *
 *   - Navigations (HTML)  : network-first, NEVER cached (pages are per-session
 *                           and sent no-store); fall back to the offline page
 *                           only when the network is unreachable.
 *   - /static/* assets    : stale-while-revalidate from a versioned cache.
 *   - Everything else      : passed straight through to the network.
 *
 * The fetch handler only ever touches same-origin GET requests and explicitly
 * steps aside for Server-Sent Events and non-GET methods, so the app's live
 * command/metric streams and form posts are untouched.
 *
 * Bump CACHE_VERSION to force every client to drop stale precached assets.
 */
'use strict';

var CACHE_VERSION = 'v3';
var STATIC_CACHE = 'nodexia-static-' + CACHE_VERSION;
var OFFLINE_URL = '/static/offline.html';

// App-shell assets warmed on install. These are the files the offline page and
// the chrome of every page need; they are also kept fresh at runtime by the
// stale-while-revalidate handler below.
var PRECACHE_URLS = [
  OFFLINE_URL,
  '/static/offline.js',
  '/static/style.css',
  '/static/fonts/exo2-latin.woff2',
  '/static/app.js',
  '/static/lucide.min.js',
  '/static/favicon.svg',
  '/static/icon-192.png',
  '/static/icon-512.png',
  '/static/icon-maskable-512.png',
  '/static/apple-touch-icon.png'
];

self.addEventListener('install', function (event) {
  event.waitUntil(
    caches.open(STATIC_CACHE).then(function (cache) {
      // Tolerate individual asset failures so one 404 never blocks install.
      return Promise.all(PRECACHE_URLS.map(function (url) {
        return cache.add(new Request(url, { cache: 'reload' })).catch(function () {});
      }));
    })
    // Note: intentionally no skipWaiting() — an updated worker waits until the
    // old clients are gone so a running session never mixes asset versions.
  );
});

self.addEventListener('activate', function (event) {
  event.waitUntil(
    caches.keys().then(function (keys) {
      return Promise.all(keys.map(function (key) {
        if (key.indexOf('nodexia-static-') === 0 && key !== STATIC_CACHE) {
          return caches.delete(key);
        }
        return null;
      }));
    }).then(function () {
      // Control the page that registered us right away so offline support works
      // on first load without a manual reload.
      return self.clients.claim();
    })
  );
});

self.addEventListener('fetch', function (event) {
  var req = event.request;

  // Only ever handle same-origin GETs. Forms, API mutations and cross-origin
  // requests fall through to the network untouched.
  if (req.method !== 'GET') return;
  var url;
  try { url = new URL(req.url); } catch (err) { return; }
  if (url.origin !== self.location.origin) return;

  // Never intercept Server-Sent Event streams (live command/metric feeds).
  if (req.headers.get('accept') && req.headers.get('accept').indexOf('text/event-stream') !== -1) {
    return;
  }

  // Page navigations: network-first with an offline fallback. Responses are not
  // cached — they may carry per-session, no-store content.
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req).then(function (resp) {
        // A response that followed a redirect cannot be handed back to a
        // navigation request (whose redirect mode is "manual"): the browser
        // rejects it as a network error and renders a BLANK page. This is the
        // cold shortcut-launch failure — opening e.g. /servers while logged out
        // bounces through a 303 to /login. Rebuild a fresh, non-redirected
        // response from the final body so the target page always renders.
        if (resp.redirected) {
          return resp.blob().then(function (body) {
            return new Response(body, {
              status: resp.status,
              statusText: resp.statusText,
              headers: resp.headers
            });
          });
        }
        return resp;
      }).catch(function () {
        return caches.match(OFFLINE_URL, { ignoreSearch: true }).then(function (cached) {
          return cached || new Response(
            'You are offline.',
            { status: 503, headers: { 'Content-Type': 'text/plain; charset=utf-8' } }
          );
        });
      })
    );
    return;
  }

  // Static assets: stale-while-revalidate.
  if (url.pathname.indexOf('/static/') === 0) {
    event.respondWith(staleWhileRevalidate(req));
  }
});

function staleWhileRevalidate(req) {
  return caches.open(STATIC_CACHE).then(function (cache) {
    return cache.match(req).then(function (cached) {
      var network = fetch(req).then(function (resp) {
        // Only cache complete, same-origin 200s (skip opaque/partial responses).
        if (resp && resp.status === 200 && resp.type === 'basic') {
          cache.put(req, resp.clone());
        }
        return resp;
      }).catch(function () {
        return cached; // offline and not cached → undefined → handled by caller
      });
      return cached || network;
    });
  });
}

/* ── Push notification foundation (server-side sender deferred) ───────────────
 * These handlers exist so an installed Nodexia app can already display a
 * notification the moment a Web Push sender is added server-side. No VAPID keys
 * or subscriptions are wired up yet — see docs/pwa.md, decision 8.
 */
self.addEventListener('push', function (event) {
  var payload = {};
  if (event.data) {
    try { payload = event.data.json(); } catch (err) { payload = { body: event.data.text() }; }
  }
  var title = payload.title || 'Nodexia';
  var options = {
    body: payload.body || '',
    icon: '/static/icon-192.png',
    badge: '/static/icon-192.png',
    tag: payload.tag || 'nodexia',
    data: { url: payload.url || '/' }
  };
  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener('notificationclick', function (event) {
  event.notification.close();
  var target = (event.notification.data && event.notification.data.url) || '/';
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(function (clients) {
      for (var i = 0; i < clients.length; i++) {
        var client = clients[i];
        if ('focus' in client) {
          client.navigate(target);
          return client.focus();
        }
      }
      if (self.clients.openWindow) return self.clients.openWindow(target);
      return null;
    })
  );
});
