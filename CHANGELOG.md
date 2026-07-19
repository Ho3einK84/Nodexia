<!-- Introduced in v0.6.0 (multi-tab workspace). -->

# Changelog

All notable changes to Nodexia are documented in this file. The format
loosely follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/);
this project does not follow strict SemVer pre-1.0, but version tags are
still `vMAJOR.MINOR.PATCH`.

## v0.6.8 — Security & quality hardening

### Security fixes

- **Scrypt DoS protection**: backup decryption now validates scrypt
  parameters (`N` must be a power of two and ≤ 2^20, `r` and `p` within
  1..32) before calling `scrypt.Key`, preventing a crafted backup envelope
  from exhausting memory.
- **Predictable session fallback removed**: `randomSessionID()` no longer
  falls back to the static `"fallback-session"` ID; the `Session`
  middleware returns HTTP 500 if a session ID cannot be generated.
- **Predictable terminal ticket ID removed**: ticket generation returns an
  empty ID and logs the error on RNG failure instead of using a guessable
  timestamp.
- **Logout CSRF fixed**: `/logout` now only accepts `POST`, and the logout
  links in the shell header and mobile drawer have been converted to
  CSRF-protected inline forms.
- **CSRF default-port normalization**: `equalHost` strips `:80` and `:443`
  suffixes so legitimate same-origin requests are not rejected when the
  browser omits the default port.
- **HSTS added**: `Strict-Transport-Security: max-age=31536000;
  includeSubDomains` is now set by the security-headers middleware.
- **CSP hardened**: `object-src 'none'` is now included in the CSP.
- **Admin password minimum length**: production/staging configurations now
  require the admin password to be at least 8 characters.
- **Login length limit**: usernames and passwords over 1024 bytes are
  rejected before the constant-time comparison to avoid DoS.

### XSS fixes

- **analytics.js**: all server-sourced values interpolated into
  `innerHTML` (chart tooltips, legend, forecast cards, trend/exhaustion
  indicators) are now escaped via `escapeHTML()`. Color values are validated
  against a hex-color regex.
- **app.js**: `bulkChip()` validates the incoming status against an
  allowlist and falls back to `"unknown"`.

### Backend fixes

- **Home handler context**: dashboard DB queries now use `r.Context()`
  instead of `context.Background()` so they are cancelled when the client
  disconnects.
- **N+1 traffic query eliminated**: the home dashboard batch-fetches latest
  traffic snapshots via the new `GetLatestTrafficByServerIDs` repository
  method instead of one query per visible server.
- **Scheduler pagination fixed**: `homeURL` now includes `sched_page` and
  the home handler reads the `sched_page` query parameter.
- **Consumed terminal ticket pruning**: `pruneExpired` now also removes
  consumed tickets older than 5×TTL, preventing a memory leak if `Release`
  is missed.
- **Metrics token log warning**: using `?token=` now logs a warning
  advising operators to prefer `Authorization: Bearer` to keep tokens out of
  access logs.
- **SSH MAC preference**: `HMAC-SHA1` is now the last-resort compatibility
  entry in the SSH MAC list.

### Middleware fixes

- **Multibyte-safe log truncation**: `safeLogValue` now truncates by rune
  instead of byte, preserving Persian/RTL text.
- **Multibyte-safe capitalization**: `friendlyError` now uppercases the
  first rune via `unicode.ToUpper` instead of the first byte.
- **Constant-time username comparison**: `RequireAuth` compares the token
  username with `crypto/subtle`.
- **Monitoring parse errors logged**: `parseFloat` and `parseInt64` now emit
  `slog.Debug` when parsing fails instead of silently returning zero.
- **Atomic alert streak increment**: `incStreak` uses a single atomic
  `UPDATE ... SET streak = streak + 1` inside a transaction, removing the
  GetStreak/SetStreak TOCTOU window.

## v0.6.7 — Mobile terminal UX + architecture documentation

### Part A: Mobile terminal — persistent tab access

The terminal's mobile layout (`terminal-card--mobile` at
`web/static/terminal.css:515`) gives the card `position:fixed` with
`z-index:1300`, making it cover the viewport and hide the tab bar. There
was no way to reach other open tabs while the terminal was fullscreen.

- Added a compact icon button (`#terminal-tabs`, `layers` icon) to the
  terminal header (`web/templates/terminal.gohtml`). It is hidden on desktop
  and shown only inside the `@media (max-width: 700px)` block
  (`web/static/terminal.css:595-631`), styled to match the existing
  `.terminal-back` button.
- The button calls `NodexiaTabs.showSwitcher()` — the same function the
  tab-bar FAB uses — so tapping it opens the existing mobile tab-switcher
  bottom sheet. Tapping a tab in the switcher calls `activate(tab.id)` and
  `hideSwitcher()`. No new tab-switching code was written; the
  `showSwitcher` / `hideSwitcher` functions are exposed on the
  `window.NodexiaTabs` public API
  (`web/static/tab-manager.js:1456-1457`).
- The terminal's WebSocket and SSH session **stay alive** while the user
  peeks at another tab: the existing `terminal-tab-adapter.js` only calls
  `pause()` on `tab-deactivated` (which sets `active = false` and gates
  resize work but keeps the WS open) and `dispose()` on `tab-closing`. So
  switching away and back is instant — no reconnect, no new SSH session,
  no error state.

### Part B: Mobile terminal — smaller default font size

The terminal's mobile default font size was 15px, which took up too much
horizontal space on a phone. Reduced the mobile default in
`web/static/terminal.js:94` from `15` to `12`. Desktop default stays at
`14`. The user-facing font-size controls (A−/A+ in the header, A−/A+ in
the mobile key toolbar, and the `Ctrl/Cmd +/-/0` keybindings) are all
preserved — only the initial default for new mobile sessions changed.
A user who already adjusted the font via the toolbar has it persisted in
`localStorage['nodexia.terminal.fontSize']` and is unaffected.

### Part C: Architecture documentation moved to docs/architecture.md

- Created `docs/architecture.md` as a real architecture map of the
  current codebase: stack overview, repository layout, request lifecycle,
  module and repository patterns, the multi-tab workspace at a high level
  (full design still in `docs/tab-system.md`), the terminal WebSocket
  lifecycle, auth/session/CSRF, bilingual UI, background work, and the
  full set of module invariants from the old CLAUDE.md.
- Added a "Documentation" section to `README.md` linking to
  `docs/architecture.md` and `docs/tab-system.md`.
- Deleted `CLAUDE.md` after folding its useful content into
  `docs/architecture.md`. The single reference to CLAUDE.md in
  `docs/tab-system.md` was updated to point at the architecture doc
  instead.

### Verification notes

- The static asset changes (CSS + JS) are embedded into the binary at
  compile time via `go:embed`, so the production rebuild
  (`docker compose build --no-cache && docker compose up -d --force-recreate`)
  is required for Parts A and B to reach mobile browsers — a container
  restart alone is not enough. The i18n catalog additions are also
  embedded; the i18n parity test passes.
- Parts A and B were verified by inspecting the rendered terminal page
  and the compiled CSS media queries; live mobile-viewport browser
  testing requires a deployed build, which is the user's deployment step.

### Regression fix: mobile scroll lock leaks to newly created tabs

**Trigger confirmed by control tests (all on the live deployment, before
this fix):**

- **Terminal open, create new tab on mobile** → new tab does not scroll,
  bottom nav is missing. **Reproduced.**
- **No terminal open, create new tab on mobile** → new tab scrolls
  normally, bottom nav present. Not reproduced (no scroll lock was set).
- **Terminal open, create new tab on desktop** → new tab works fine.
  Not reproduced (Part A's mobile fullscreen layout only activates at
  `matchMedia('(max-width: 700px)').matches`).

These control tests pinpoint Part A's mobile fullscreen state as the
sole trigger, and rule out tab creation itself or any other change.

**Root cause:** `terminal.js`'s `enableMobile()` calls `setScrollLock(true)`
once on script load, which adds the `terminal-mobile-active` class to
`<html>` and `<body>`. The CSS at `web/static/terminal.css:576-591`
uses that class to set `overflow: hidden` on both elements and to hide
`.bottom-nav`. The class was only ever removed on explicit
`terminal-back` click, `reconnect()`, or `pagehide` — **never on tab
switch**. So once a mobile terminal had connected, every subsequent tab
activation inherited `html/body { overflow: hidden }` and a hidden
bottom nav, regardless of which tab was now active. A full page refresh
cleared it (because the new load went through `enableMobile()` again
only for the terminal tab, and the non-terminal tabs never set the
class), which is why refresh "fixed" it.

**Fix:** Scope the scroll lock to the terminal tab's *active* state, not
its lifetime. Moved the `setScrollLock` calls into the
`card.__nodexiaTerminal` lifecycle methods in `web/static/terminal.js`:

- `pause()` (fired by `terminal-tab-adapter.js` on `tab-deactivated`)
  now calls `setScrollLock(false)` on mobile, so the lock is released
  the moment any other tab becomes active.
- `resume()` (fired on `tab-activated`) calls `setScrollLock(true)` on
  mobile, so the lock is re-acquired only when the terminal tab is the
  active one.
- `dispose()` (fired on `tab-closing`) also calls `setScrollLock(false)`
  as a safety net so closing the terminal tab never strands the lock.

No CSS or template changes; the existing `terminal-mobile-active` class
is now simply toggled in sync with the tab's active state instead of
being set once and forgotten. The terminal-card--mobile class on the
card itself is unchanged and remains scoped to the terminal pane.

### Polish: mobile tab-switcher sheet (font, radius, icon chips)

The mobile tab-switcher bottom sheet (the one the Part A tab-access
button and the FAB open to list open app-tabs) was inheriting the app's
UI font in theory but didn't declare it explicitly, so on some browsers
the dialog-like container fell back to a system font. It also used
small `--radius-sm` corners and flat monochrome icons that didn't match
the softer, pill-shaped style used elsewhere in the panel.

Changes in `web/static/tabs.css` (mobile tab-switcher section) and
`web/static/tab-manager.js` (`renderSwitcherGrid`):

- **Font:** declared `font-family: var(--font-ui)` and
  `-webkit-font-smoothing: antialiased` on `.tab-switcher` so the sheet
  and every descendant consistently use the app's Exo 2 / Vazirmatn
  stack regardless of the browser's dialog default.
- **Corner radius:** bumped to a softer shape across the component —
  `24px` on the sheet's top corners, `999px` (pill) on the header
  action buttons (`+ New tab`, `Close all`, the `X` dismiss), and
  `16px` on each tab row card and on the per-row close button.
- **Tinted icon chips:** each row's leading icon is now a
  `36×36` rounded-square chip, matching the icon treatment used by
  the Overview stat cards. Generic tabs use the blue accent tint
  (`rgba(59,130,246,0.14)` background, `--accent-soft` icon). Terminal
  tabs use a violet tint when idle and a green tint with a subtle ring
  shadow when connected, so the kind and connection state are
  recognisable at a glance. `tab-manager.js` adds the
  `tab__icon--terminal` and `is-connected` classes accordingly.
- **Active state indicator:** the green status dot on a connected
  terminal row now has a soft `0 0 6px` glow so it reads as part of
  the row rather than an afterthought.
- **Touch feedback:** the per-row close button and the top action
  buttons all get explicit `:active` background changes (red tint for
  close, scale for the rest) for clear pressed-state feedback in this
  touch-only context. `-webkit-tap-highlight-color: transparent` and
  `touch-action: manipulation` on the card prevent the iOS flash and
  300ms tap delay respectively.

---

## v0.6.6 — Form encoding CSRF fix + stale service-worker cache fix

### Root cause confirmed (delete "Access denied" + terminal not rendering)

Both the "Access denied" error on server deletion and the terminal not
rendering after connect share a single root cause: **the tab-manager sent
all POST forms as `multipart/form-data`**, which Go's CSRF middleware cannot
parse.

The chain:

1. `tab-manager.js` intercepts every `<form>` submit inside a tab pane and
   sends it via `fetch()` with `new FormData(form)` as the body.
2. `fetch()` with a `FormData` body automatically sets `Content-Type:
   multipart/form-data; boundary=…`.
3. The CSRF middleware calls `r.ParseForm()`, which (in Go's `net/http`) only
   parses `application/x-www-form-urlencoded` bodies. For `multipart/form-
   data`, `ParseForm` silently succeeds but populates nothing.
4. `r.FormValue("_csrf_token")` returns `""`, the `submitted == ""` check
   fires, and the middleware responds **403 "csrf: invalid token"**.

This broke every POST form in the tab system — server create/edit, terminal
credentials, delete, alert rules, etc. The delete and terminal flows were the
most visible because they were explicitly tested.

Confirmed live against production with `curl`:
- `multipart/form-data` POST → **403** "csrf: invalid token"
- `application/x-www-form-urlencoded` POST → **303** redirect (success)

### Fixed

- **`tab-manager.js`: POST forms now send `application/x-www-form-urlencoded`
  instead of `multipart/form-data`.** `new FormData(form)` is converted to
  `new URLSearchParams(new FormData(form))`, which `fetch()` encodes as URL-
  encoded key-value pairs. Forms with `enctype="multipart/form-data"` (the
  backup-restore form) are exempted and keep multipart encoding — that form
  already carries the CSRF token in the URL query string as a workaround.
- **`tab-manager.js`: same-URL navigation dedup is now GET-only.** The
  `navigateInPane()` early-return that skips `fetch()` when the target URL
  matches the current tab URL now also checks the HTTP method. POST/PUT/PATCH
  forms whose action equals the current tab URL were being silently
  no-op'd: `e.preventDefault()` had already stopped the native form
  submission, then the dedup branch returned a resolved Promise, and
  `restoreFormUI` cleared the loading overlay without ever hitting the
  backend. This affected the three forms whose action is the current page:
  "Refresh Snapshot" (`/servers/{id}/monitoring`), "Run Discovery"
  (`/servers/{id}/nodes`), and "Open Terminal" (`/servers/{id}/terminal`).
  Fixed by adding `&& method === 'GET'` to the dedup guard so non-GET
  navigations always re-fetch.

### Also in this release (service-worker cache fix)

- **Service worker `CACHE_VERSION` bumped from `'v6'` to `'v7'`.** This
  forces every client to drop the old `nodexia-static-v6` cache and re-fetch
  all assets from the new binary.
- **`skipWaiting()` added to the install handler.** The new service worker
  now activates immediately instead of waiting for all old tabs to close.
  Combined with `clients.claim()` (already present in the activate handler),
  updated assets reach all open tabs on the next navigation.
- **`PRECACHE_URLS` updated** to include `tabs.css` and `tab-manager.js`
  (core multi-tab workspace assets introduced in v0.6.0).

### Deployment note

After pulling this release, a **full forced rebuild** is required — a
container restart is not sufficient because `go:embed` bakes files into the
binary at compile time:

```bash
docker compose build --no-cache && docker compose up -d --force-recreate
```

In the browser, verify the new service worker is active via DevTools →
Application → Service Workers (should show `nodexia-static-v7`). If old
assets persist, use DevTools → Application → Storage → Clear site data.

## v0.6.5 — Terminal hang, delete-server, loading overlay, tab UI fixes

### Fixed

- **Terminal WebSocket and xterm not disposed when closing the initial
  terminal tab.** `terminal-tab-adapter.js` resolved its pane reference via
  `document.currentScript.closest('.tab-pane')` at script load time. On the
  initial page load (direct URL visit), the PageScripts are rendered in
  `<body>` outside `#tab-content` — the tab-manager wraps `#tab-content`
  children into a pane, but the scripts remain in `<body>`. So
  `.closest('.tab-pane')` returned `null`, and the adapter's `tab-closing`
  event handler (which checks `event.detail.pane === pane`) never matched
  the real pane. The WebSocket and xterm instance leaked until the server's
  30-second ping timeout cleaned them up. Fixed by resolving the pane lazily
  from the active tab record or `card.closest('.tab-pane')` on first use,
  instead of caching a stale `null` at load time.
- **Loading overlay ("Working over SSH…") can get stuck after form
  submission through the tab system.** The `#loading-overlay` is a
  full-screen `position:fixed;z-index:9999` element shown by app.js's
  submit handler. The tab-system's `restoreFormUI` hides it after the fetch
  completes, but two edge cases could leave it visible: (1) if the fetch
  promise never settled (server hang, unhandled rejection), and (2) if the
  user cancelled a `data-confirm` dialog after `startTopBar()` had already
  fired, leaving the progress bar stuck. Fixed by: adding a 15-second
  safety timeout to the overlay in app.js's submit handler; wrapping
  `restoreFormUI`'s overlay hide in try/catch so it never fails silently;
  and calling `finishTopBar()` in the confirm-cancel path.
- **Tab close button shows a boxed outline on Android/mobile.** Added
  `-webkit-tap-highlight-color: transparent` to `.tab__close` (both desktop
  and mobile) to suppress the browser's default tap highlight, which on
  Android renders as a visible rectangle around the button.

### Changed

- `terminal-tab-adapter.js` now resolves the pane lazily via
  `NodexiaTabs.getActive()` or `card.closest('.tab-pane')` instead of
  caching `document.currentScript.closest('.tab-pane')` at load time.
- `app.js` `initForms` now calls `finishTopBar()` when a `data-confirm`
  dialog is cancelled.
- `app.js` loading overlay now auto-hides after 15 seconds as a safety net.
- `tab-manager.js` `restoreFormUI` wraps the overlay hide in try/catch.

## v0.6.4 — Terminal hang fix, delete-server CSRF fix, tab UI polish

### Fixed

- **Terminal tab no longer hangs on "Working over SSH…" indefinitely.**
  A 30-second connection timeout was added to `terminal.js`. If the WebSocket
  does not open within 30 seconds (server unreachable, ticket expired, network
  issue), the terminal now shows an explicit error message and offers a
  Reconnect action instead of staying stuck on the connecting state forever.
- **Deleting a server from within a tab no longer returns "Access denied".**
  The tab system's form interception now fetches a fresh CSRF token from a new
  `GET /api/csrf-token` endpoint immediately before every POST submission. This
  eliminates a race where the session cookie could be refreshed between the
  page load (which embedded the CSRF token) and the form POST, causing the
  embedded token to no longer match the live session and the CSRF middleware
  to reject the request with 403.
- **Tab close button no longer shows a boxed outline on Android/mobile.**
  The close ("×") button on each tab now uses a circular hover/active
  background instead of a hard-edged rectangular outline. The focus-visible
  indicator uses a circular box-shadow instead of a square outline. The touch
  target remains 44×44px on mobile for adequate tappability.

### Added

- **`GET /api/csrf-token` endpoint.** Returns the current session's CSRF token
  as JSON (`{"csrf_token":"..."}`). Used by the tab system to refresh stale
  tokens before form submissions. Requires authentication (goes through the
  standard middleware chain).

## v0.6.3 — Multi-tab bug fixes (failed tabs, resource leaks, slowness)

### Fixed

- **Non-2xx responses with valid HTML no longer show "Failed to load this tab".**
  `loadPane` rejected *any* non-OK HTTP status (502, 422, 404, 500) with a
  generic error, discarding the server's rendered error page. Now it checks
  whether the response body contains the `#tab-content` wrapper; if it does,
  the error page is injected into the pane so the user sees the real error
  (SSH failure detail, validation errors, not-found page) instead of a
  meaningless "Failed to load" message. Only responses without `#tab-content`
  (true network errors, bare 403s) fall back to the error placeholder, which
  now shows a **specific message** based on the status code (server error,
  not found, access denied, or `HTTP {status}`).
- **"Run Discovery" tab no longer fails when SSH discovery errors.** The nodes
  handler returns 502 when the SSH collection fails; the old `loadPane`
  rejected this as a load failure. The 502 body now renders normally,
  showing the discovery error output inside the tab.
- **`terminal-tab-adapter.js` is now loaded on the terminal page.** The file
  existed since v0.6.0 but was never added to the terminal page's
  `PageScripts` list, so terminal tabs never received `pause`/`resume`/
  `dispose` lifecycle calls — closing a terminal tab leaked the WebSocket
  and xterm instance, and backgrounded terminals kept running at full speed.
- **Full-page `window.location.href` / `window.location.reload()` calls
  replaced with tab-aware navigation.** Seven call sites in `app.js`
  (`initAutoRefresh`, `initManualRefresh`, `dismissNodeCard`,
  `reloadForResult`, `initShortcuts`) and two in `terminal.js` (reconnect,
  back button) navigated the *entire* browser page, blowing away the
  multi-tab shell and cancelling in-flight tab fetches (which then showed
  "Failed to load this tab"). All now route through `NodexiaTabs.navigate()`
  / `NodexiaTabs.reloadActive()`, falling back to `window.location` only
  when the tab system is not active.
- **SSE EventSource connections are closed on tab close.** `initStream` and
  `initBulkStream` created `EventSource` instances that were never cleaned
  up when a tab was closed or replaced. A new `registerCleanup` mechanism
  attaches teardown callbacks to the pane element; `tab-manager.js` runs
  them before replacing or closing the pane. The auto-refresh countdown
  interval and node-result dismiss interval are also registered for cleanup.
- **`FormData` now includes the submitter button's name/value.**
  `initFormInterception` used `new FormData(form)` (without the submitter),
  so forms whose handler depends on the clicked button's `name` — notably
  node action buttons (`name="action"`) — silently lost the action key.
  Now uses `new FormData(form, submitter)`.
- **Missing init functions added to `rescan()`.** Tab panes loaded via
  `fetch + DOMParser` never ran through `boot()`, so the following features
  were dead inside tabs: manual refresh buttons (`data-refresh-now`),
  auto-refresh dropdown, node-result auto-dismiss, node credentials show/
  hide, server/node action menus, and advanced-panel toggles. All are now
  called in `rescan()` with pane-scoped roots and idempotency guards.

### Improved

- **UI stays smooth with multiple tabs open.** The combination of leaked
  SSE connections, un-disposed terminal WebSockets, and full-page
  navigations from background tabs caused accumulating reflow/repaint
  overhead. With cleanups and tab-aware navigation in place, 4–5
  simultaneous tabs no longer degrade responsiveness.

## v0.6.2 — Comprehensive tab system review, bug fixes, and polish

> **Release status:** this entry describes the change set as implemented and
> reviewed. The version string itself (`Makefile`, `scripts/install.sh`,
> `.env.production.example`, CI) is bumped separately by a maintainer running
> `make release VERSION=v0.6.2` (the existing `scripts/release.sh`) once
> `make test` passes — that step is not part of this change set and has not
> been run yet.

### Fixed

- **Bottom navigation highlight now uses prefix matching.** Nav highlight
  updates after in-pane navigation correctly highlight the deepest matching
  top-level section (e.g. `/servers` stays active when navigating to
  `/servers/123/monitoring`), instead of requiring an exact URL match that
  left server-scoped routes unhighlighted.
- **Progress bar timing is now correct.** The top progress bar's completion
  delay is reduced, and it now finishes when a new tab's content actually
  loads rather than immediately after the tab is created. In-pane mobile
  navigations still finish the bar after the fetch completes, so it never
  appears "stuck" longer than the actual load.
- **In-flight fetches are cancelled on navigation/reload.** Each tab now uses
  an `AbortController` so a new navigation or reload aborts the previous
  fetch instead of racing and potentially overwriting newer content.
- **Form UI is restored when navigation is vetoed.** If a pane's
  `tab-closing` event cancels an in-tab form submission, the loading overlay,
  busy button, and progress bar are now properly restored.
- **Back/forward no longer reloads identical URLs.** `navigateInPane` skips
  teardown and re-fetch when the target URL is the same as the tab's current
  URL, preventing unnecessary terminal disposal and pane rebuilds.
- **Long-press no longer fires while scrolling.** Both tab and link
  long-press handlers now cancel when vertical scroll intent is detected,
  eliminating accidental context-menu triggers during normal page scrolling.
- **Long-press tolerance increased** from 10 px to 16 px for a more forgiving
  mobile gesture.
- **Middle-click on a tab now closes it**, matching desktop browser
  conventions.
- **Long-pressing a tab's close button no longer opens the context menu.**
- **Keyboard shortcuts are disabled while the tab switcher is open.**

### Improved

- **Mobile tab switcher is significantly more polished:**
  - Added a drag handle and swipe-down-to-dismiss gesture.
  - Added a focus trap so Tab cycles within the switcher while open.
  - Focus moves to the first card when opened and returns to the FAB when
    closed.
  - Cards are keyboard-focusable and activatable with Enter/Space.
  - Added an empty-state message when no tabs are open.
  - Body scroll is locked while the switcher is open.
- **Context menus and link action sheets support keyboard navigation.**
  Arrow keys move between items, Home/End jump to first/last, Enter/Space
  activate, and Escape closes the menu and returns focus to the originating
  element.
- **In-pane navigation resets scroll to top** so a new page doesn't inherit
  the previous page's scroll position.
- **Pinned tabs show a tooltip** with their full title on hover/focus.
- **Loading state has a visible spinner** in addition to the pulse animation.
- **Focus-visible outlines added** for tabs, the New Tab button, close
  buttons, switcher header buttons, switcher cards, and floating menu items,
  improving keyboard accessibility.
- **Switcher header buttons have `:active` scale feedback.**
- **New `js.tabs.no_tabs` localized string** in English and Farsi.

## v0.6.1 — Tab system polish & bug fixes

> **Release status:** this entry describes the change set as implemented and
> reviewed. The version string itself (`Makefile`, `scripts/install.sh`,
> `.env.production.example`, CI) is bumped separately by a maintainer running
> `make release VERSION=v0.6.1` (the existing `scripts/release.sh`) once
> `make test` passes — that step is not part of this change set and has not
> been run yet.

### Fixed

- **Bottom navigation highlight now updates after in-pane navigation.**
  When navigating to a new page via a link click on mobile (in-pane fetch),
  the bottom nav's active state, desktop shell nav, and drawer links now
  correctly reflect the current URL instead of staying stuck on the previous
  page's highlight.
- **Progress bar no longer gets stuck after tab navigation.** The top
  progress bar (started by `app.js` on link/form clicks) is now properly
  finished when the tab system intercepts the navigation and loads content
  via fetch instead of a full page reload.
- **Loading overlay dismissed after tab form submissions.** In-tab form
  submissions that are intercepted by the tab system now correctly hide the
  loading overlay and restore the submit button state after the fetch
  completes.
- **Tab context menu and link action sheet now have proper styling.** The
  floating context menu (right-click/long-press on tabs) and link action
  sheet (long-press on links, mobile) were missing CSS — they now render
  with a polished, frosted-glass appearance with proper hover/active states.
- **Tab toast notification now styled.** The toast used for mobile tab cap
  notices and other tab-system messages now has proper positioning,
  animation, and visual styling.
- **Tab switcher card close button properly styled.** The close button in
  the mobile tab switcher cards now picks up the shared `.tab__close`
  styling with correct grid placement and touch target sizing.
- **Tab pane loading/error states styled.** The loading spinner and error
  retry UI shown inside a tab pane during fetch now have proper centered
  layout and animation.

### Improved

- **Desktop tab close buttons less visually noisy.** Close buttons on
  inactive tabs are now hidden until hover, reducing visual clutter while
  keeping the active tab's close button always visible.
- **Active tab has a bottom accent indicator.** A subtle accent-colored bar
  at the bottom of the active tab provides an additional visual cue beyond
  background color and border.
- **Drag-to-reorder has better visual feedback.** Dragged tabs now show
  reduced opacity and a slight scale-down, while valid drop targets
  highlight with an accent border.
- **Tab pane switch has a subtle fade-in transition.** Switching between
  tabs now animates with a brief opacity transition instead of an abrupt
  content swap.
- **Mobile tab switcher uses 2-column grid on wider screens.** Phones with
  viewports ≥ 420px now show tab switcher cards in a 2-column grid, making
  better use of screen real estate.
- **Tab switcher cards have staggered entrance animations.** Cards animate
  in with a slight stagger for a more polished, sequential reveal.
- **Active tab in switcher is visually distinguished.** The currently active
  tab's card has a highlighted border and background to make it immediately
  identifiable.
- **Tab switcher card close buttons are more prominent.** Larger touch
  targets with better visual feedback on tap.
- **FAB badge is larger and has a subtle shadow.** The tab count badge on
  the mobile FAB is slightly larger with a blue glow shadow for better
  visibility.
- **Tab bar has a subtle inner highlight.** A faint top inner shadow gives
  the tab bar a more refined, layered appearance.
- **Switcher sheet header has a subtle background tint.** The header area of
  the mobile tab switcher sheet now has a slightly darker background for
  better visual separation from the card grid.
- **Reduced-motion preferences respected for all new animations.** Pane
  transitions, card entrances, toast animations, and floating menu
  animations are all disabled under `prefers-reduced-motion: reduce`.

## v0.6.0 — Multi-tab workspace

> **Release status:** this entry describes the change set as implemented and
> reviewed. The version string itself (`Makefile`, `scripts/install.sh`,
> `.env.production.example`, CI) is bumped separately by a maintainer running
> `make release VERSION=v0.6.0` (the existing `scripts/release.sh`) once
> `make test` passes — that step is not part of this change set and has not
> been run yet.

### Added

- **Multi-tab workspace.** Open several pages at once in a persistent tab
  bar (desktop/tablet) or a bottom-sheet tab switcher + floating action
  button (mobile, `< 768px`), switch between them instantly with no
  reload, and reorder, pin, duplicate, reload, or close them individually
  or in bulk (Close Others / Close All) via right-click or long-press.
  Tabs restore automatically after a page refresh; the most recently
  closed tab can be reopened. See `docs/tab-system.md` for the full
  write-up.
- Tabs are built entirely client-side: a tab fetches the exact same full
  page a direct URL visit would return and extracts its content, so there
  are **no backend rendering changes** — every page keeps working
  identically with JavaScript disabled or when linked to directly.
- **Terminal tabs are fully independent.** Multiple terminal sessions
  (to the same or different servers) can be open concurrently, each with
  its own live WebSocket, PTY, and xterm scrollback; switching tabs never
  reconnects or consumes a new single-use ticket, and a backgrounded
  terminal tab keeps receiving output while skipping unnecessary
  resize/measurement work. The existing per-user concurrent terminal
  session cap is unchanged.
- Keyboard shortcuts for tab management (new tab, close, next/previous,
  reopen closed, pin, duplicate, switcher, jump-to-tab-N) — see
  `docs/tab-system.md` for the full table and which bindings are
  guaranteed vs. best-effort.
- Mobile-specific behavior: a 5-concurrent-tab cap with automatic eviction
  of the oldest background tab, swipe-to-switch on the content area,
  long-press context menu and link "open in new tab" action sheet, and a
  dedicated tab switcher sheet reachable from a FAB.
- New `js.tabs.*` localized strings (English + Farsi).

### Known limitations

- **Only `terminal.js` got a multi-instance-safe rewrite.** Every other
  page-specific script (`monitoring.js`, `files.js`, `analytics.js`,
  `commands.js`, `bulk.js`, `livemetrics.js`, …) is still a document-global
  singleton. Opening the *same* non-terminal route in two tabs at once
  renders correct server-provided data/markup in both, but only one such
  pane's *live* JS behavior (e.g. an SSE stream hookup, the live-metrics
  WebSocket, an auto-refresh countdown) is guaranteed wired at a time —
  whichever pane was created/activated most recently. This never corrupts
  data (the underlying job keeps running server-side) and a tab's own
  Reload action always re-fetches fresh state. This is a deliberate v0.6.0
  scope boundary, documented as the upgrade path for a future release in
  `docs/tab-system.md`.
- **Split view is a stub only.** Reserved DOM container, CSS class
  scaffold, and an undispatched event name (`tab-split-requested`) exist
  for a future release; no side-by-side split UI ships in v0.6.0.
- `Ctrl+T` / `Ctrl+W` / `Ctrl+Tab` / `Ctrl+Shift+Tab` / `Ctrl+PageUp` /
  `Ctrl+PageDown` / `Ctrl+Shift+T` / `Ctrl+1..8` could not be used as real
  bindings — every major browser reserves them for its own native tab
  strip and page JavaScript cannot reliably intercept them. Nodexia ships
  `Alt`-based shortcuts as the real, guaranteed bindings instead, and
  attempts the `Ctrl`-based ones best-effort (they only tend to work when
  Nodexia is installed as a standalone PWA window, which has no native
  browser tab strip to compete with).
