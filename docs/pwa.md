# Progressive Web App (PWA) support

This document records the architecture decisions behind Nodexia's PWA layer so
future changes stay aligned with the project's constraints (Go 1.25, stdlib HTTP,
SSR with `html/template`, few dependencies, minimal `web/static`).

## Goals

- Installable on mobile and desktop (Add to Home Screen / install prompt).
- App-like launch (standalone window, branded splash, theme-colored status bar).
- A polished, reliable mobile experience for operators who check the dashboard
  on the go.
- No new runtime dependencies and no client-side framework — the implementation
  is a thin, progressive-enhancement layer over the existing SSR pages.

## Priorities (as required)

1. Reliability  2. Maintainability  3. User experience  4. Minimal complexity

Every decision below is justified against these, in order.

## What ships

| Asset | Path | Served by |
| --- | --- | --- |
| Web app manifest | `/manifest.webmanifest` | `handlers.ManifestHandler` (dynamic, reflects `App.Name`) |
| Service worker | `/sw.js` | `handlers.ServiceWorkerHandler` (root scope) |
| Offline fallback | `/static/offline.html` | static FS + SW precache |
| App icons | `/static/icon-192.png`, `icon-512.png`, `icon-maskable-512.png`, `apple-touch-icon.png`, `favicon.svg` | static FS |
| Shortcut icons | `/static/shortcut-servers.png`, `shortcut-diagnostics.png`, `shortcut-alerts.png` (96×96) | static FS |

The service worker source lives in `web/static/sw.js` (a normal, lintable JS
file embedded via the existing `//go:embed web/static/*`). It is *served* from
the site root so its default control scope is the whole origin.

## Key decisions and reasoning

### 1. Service worker served from root, not `/static/`

A worker's default scope is its own URL path. Serving it from `/static/sw.js`
would scope it to `/static/` and it could not control navigations. A dedicated
`GET /sw.js` handler returns the embedded file with `Service-Worker-Allowed: /`
and `Cache-Control: no-cache`, giving whole-origin scope and prompt update
checks. This is the smallest reliable option and avoids touching the static file
server.

### 2. Navigations: network-first, never cached

Authenticated pages carry sensitive, per-session data and are sent with
`Cache-Control: no-store`. The service worker therefore **never caches HTML
navigations**. It tries the network first and, only when the network is
unreachable, serves the precached, static **offline page**. This keeps secrets
out of the cache (reliability + security) while still giving a graceful offline
state.

### 3. Static assets: stale-while-revalidate

`/static/*` assets (CSS, JS, icons, vendored xterm/lucide) are content that the
app shell needs. They are served cache-first for instant loads, with a
background revalidation that picks up new versions after a deploy. A versioned
cache name (`nodexia-static-v<N>`) is purged on `activate`, so bumping the
version constant in `sw.js` is the one-line lever for a hard refresh.

### 4. Scope of interception is deliberately narrow

The `fetch` handler only ever handles **same-origin GET** requests, and only for
navigations and `/static/`. It explicitly bypasses:

- non-GET requests (forms, API mutations),
- Server-Sent Events (`Accept: text/event-stream` — live command/metric streams),
- WebSocket upgrades (terminal, live metrics — these never hit `fetch` anyway),
- cross-origin requests.

Everything else falls through to the network untouched. This protects the
existing SSE/WS-heavy features from any caching surprises.

### 5. Update strategy: no `skipWaiting`, `clients.claim` on activate

An updated worker waits until existing clients are gone before activating, so a
running session never sees a half-updated asset set. `clients.claim()` on
activate lets the *first* install start controlling the current page immediately,
so offline support works without a manual reload. No intrusive "update
available" UI — full-page navigations naturally pick up the new worker.

### 6. Manifest is rendered dynamically

`App.Name` is configurable (`NODEXIA_APP_NAME`) and used throughout the SSR UI.
The manifest handler reflects it in `name`/`short_name` so an operator who
rebrands the panel gets a consistent installed-app name. The handler is ~30
lines of stdlib JSON encoding — no template, no dependency. It is exempt from
auth so the browser (which fetches the manifest without credentials) can read it
from the login page.

### 7. Icons generated, committed, and reproducible

No image toolchain is assumed in CI. Icons are generated once by a stdlib-only
Go program (`scripts/genicons/main.go`, `//go:build ignore`) and the resulting
PNGs are committed. `favicon.svg` is hand-authored for crisp scalable rendering.
Run `go run scripts/genicons/main.go` to regenerate after a brand change.
A maskable 512px icon (full-bleed background, motif inside the 80% safe zone)
covers Android adaptive icons; rounded 192/512 cover everyone else; a 180px
opaque `apple-touch-icon.png` covers iOS. The same generator also renders the
per-shortcut icons (decision 10).

### 8. Push notifications: foundation only, server side deferred

Nodexia already delivers alerts through its Telegram channel pipeline. A full
Web Push stack (VAPID keys, subscription storage + migrations, an RFC 8291
encrypted-payload sender) is a large, security-sensitive surface that would add
real maintenance cost for a capability the alerting system already covers. So:

- The service worker **includes `push` and `notificationclick` handlers** so the
  client foundation exists and an installed app can display a notification the
  moment a server-side sender is added.
- No VAPID keys, subscription endpoints, or DB schema are added now.

Revisit when there's a concrete need for browser-native (vs Telegram) alerts.

### 9. Manifest shortcuts ship per-shortcut icons

Each `shortcuts` entry (Servers, Diagnostics, Alerts) declares its **own `icons`
array** with a 96×96 PNG — the Android baseline for shortcut icons — plus a
`short_name` and `description`. This is a spec requirement, not a nicety: a
shortcut with no `icons` renders a **blank white placeholder** in the launcher
(only the app's own glyph shows in a corner), and a malformed shortcut pinned to
the home screen launches poorly. The three icons are distinct, recognizable
glyphs on the shared branded background (a stacked list, a heartbeat, an
exclamation mark), rendered by the same stdlib generator as the app icons
(`scripts/genicons/main.go`) so there is no parallel toolchain and the PNGs are
committed and reproducible. Distinct icons were cheap to produce from the
generator's existing primitives, so they were preferred over a single shared
shortcut icon for clearer launcher presentation.

### 10. Cold shortcut launch must never render blank

Opening a shortcut while logged out navigates to a protected page (e.g.
`/servers`), which the auth middleware answers with a `303 → /login`. The service
worker's navigate handler is network-first, and a navigation request carries
redirect mode `manual`; handing a **followed/redirected** response back to such a
request makes the browser reject it as a network error and paint a **blank
page**. The handler now detects `response.redirected` and rebuilds a fresh,
non-redirected `Response` from the final body, so the auth bounce always renders
the login page (and any future redirect-based navigation is equally safe). A
test asserts each shortcut target cleanly returns `303 → /login` rather than a
404 or an empty body.

### 11. Orientation: locked to portrait, never rotates

The manifest declares **`"orientation": "portrait"`**. The app must stay upright:
on a phone turned sideways it should not rotate to landscape at all, even when the
device's system rotation lock is off. `"portrait"` is chosen over
`"portrait-primary"` because it is the more broadly honoured value across Android
Chromium installs while still keeping the app upright; `portrait-primary` would
additionally forbid the (rarely auto-triggered) upside-down portrait but is no
more reliable in practice. The earlier "omit the member so the OS rotation lock
wins" approach was abandoned — it left rotation entirely to the device, which
still let the installed app swing to landscape and did not satisfy the
"never rotate" requirement.

**Defence-in-depth runtime lock.** Because some browsers/installs do not fully
honour the manifest field, `app.js` (`initOrientationLock`) also calls
`screen.orientation.lock('portrait')` at boot. It is strictly best-effort and
fully feature-detected: the API is absent on iOS Safari (so the call is skipped),
and Chromium only permits the lock for an installed/fullscreen app (so the
returned Promise can reject). Every path is wrapped so a failure is swallowed and
**never throws or breaks the page**. This adds a small, self-contained snippet to
`web/static`, justified by the reliability win on platforms that ignore the
manifest member.

**The terminal.** There is no longer a landscape exception for the in-browser SSH
terminal — it runs in portrait like everything else. The terminal's
`orientationchange` listener (`terminal.js`) is now effectively a no-op on locked
clients (orientation no longer changes) but is kept as a harmless safety net: if
any browser still allows a flip, the xterm grid reflows correctly rather than
being left mis-sized. The bfcache scroll-lock release on `pagehide` is unaffected.

**iOS caveat.** iOS Safari has historically ignored the manifest `orientation`
field for home-screen PWAs and exposes no `screen.orientation.lock()`, so neither
mechanism can *force* portrait there — on iOS the app follows the device's own
rotation behaviour. The runtime lock therefore buys us nothing on iOS; it is the
Android/Chromium path that benefits. The manifest value remains correct and
harmless on iOS.

Note: installed PWAs cache the manifest, so an already-installed app must be
**reinstalled** (or its manifest cache cleared) before this change takes effect.

### 12. CSP unchanged

The existing policy (`default-src 'self'`, `script-src 'self'`) already permits
a same-origin worker (`worker-src` falls back to `default-src`), the manifest
(`manifest-src` → `default-src`), `connect-src 'self'` for SW fetches, and
`img-src 'self' data: https:` for icons. No policy change was required —
confirming the PWA fits the security model rather than bending it.

## Mobile UX touch-ups bundled in

- `viewport-fit=cover` so the existing `env(safe-area-inset-*)` rules in
  `style.css` actually take effect inside the standalone window on notched
  devices.
- `theme-color`, `apple-mobile-web-app-*`, and `application-name` meta tags for a
  native status bar and launch title.
- Manifest `shortcuts` (Servers, Diagnostics, Alerts) for long-press app menus,
  each with its own 96×96 icon, `short_name`, and `description` (decision 9).
