<!-- Introduced in v0.6.0 (multi-tab workspace). -->

# Multi-tab workspace

v0.6.0 adds a browser-tab-style workspace on top of Nodexia's server-rendered
pages: a tab bar (desktop) / bottom-sheet switcher + FAB (mobile) that lets you
have several pages open at once — e.g. one server's monitoring page, another
server's terminal, and the alerts list — switching between them instantly
without a full page reload.

This document covers how it works, how to make a future page-specific script
"tab-aware" the way `terminal.js` already is, and mobile-specific behavior. It
is a summary for readers/maintainers, not the implementation spec — see the
in-repo source (`web/static/tab-manager.js`, `web/static/tabs.css`,
`web/static/terminal-tab-adapter.js`) for exact behavior.

## Architecture

Each tab is a plain `<div class="tab-pane">` created, shown/hidden, and
destroyed by `tab-manager.js`. There is deliberately **no `<iframe>`** — the
app already sends `X-Frame-Options: DENY` and a `frame-ancestors 'none'` CSP,
so loading Nodexia's own pages in a frame (even same-origin) would be blocked
by the browser unless that clickjacking hardening were weakened app-wide,
which this feature does not do.

Opening a tab does **not** add a new server-side render path. `tab-manager.js`
runs a plain `fetch()` against the tab's URL — the exact same full HTML
document a direct browser visit to that URL would receive — then uses
`DOMParser` to pull out the children of `#tab-content` from the fetched
document and drops them into a new `.tab-pane`. Because it's the same
render path every time, a direct URL visit and a tab both always show
identical content; there is nothing to keep in sync, and JS-disabled clients
see the pre-v0.6.0 experience unchanged (the tab bar stays `hidden` until
`tab-manager.js` successfully initializes).

The one wrinkle with fetching a *whole page* to build a tab pane: a page's own
`PageStyles`/`PageScripts` (e.g. `terminal.css`/`terminal.js`,
`analytics.js`, `bulk.js`) render as `<link>`/`<script>` tags outside
`#tab-content`, so a naive extraction would silently drop every module's CSS
and JS behavior. `tab-manager.js` corrects for this on every tab open:

1. Any `<link rel="stylesheet">` in the fetched `<head>` not already present
   in the real document's `<head>` (deduped by exact `href`, which already
   includes a cache-busting hash) is appended once, globally — stylesheets
   are additive and never removed when a pane closes.
2. Every `<script src>` that follows `/static/app.js` in the fetched
   document (i.e. that page's own `PageScripts` chain) is cloned as a fresh
   `<script>` element and appended **inside the new `.tab-pane`** itself,
   with `async = false` so multi-file chains (like terminal's xterm + addons
   + `terminal.js`) keep their relative execution order. Appending inside the
   pane — not `<head>`/`<body>` — is what makes
   `document.currentScript.closest('.tab-pane')` resolvable, which is the
   whole basis for making a script pane-aware (see below). A page's scripts
   run fresh every time a new pane for that route is created, even if the
   exact same URL already ran once for an earlier, still-open tab.

Tab metadata (id, url, title, icon, pinned, order, kind) persists to
`sessionStorage` so a page refresh restores which tabs/URLs were open. Live
state — WebSocket connections, xterm scrollback, in-flight SSE streams —
never survives a real page reload and isn't meant to; that's the browser
reloading, not a tab switch.

## Known limitation: only `terminal.js` is safe with two tabs of the same route open

Every existing per-page script (`monitoring.js`, `files.js`, `analytics.js`,
`commands.js`, `bulk.js`, `livemetrics.js`, and — before v0.6.0 —
`terminal.js` too) was written as a document-global singleton: it resolves
its elements once, at execution time, via bare `document.getElementById(...)`
/ `document.querySelector(...)` against the *whole* document, and assumes
there is exactly one instance of itself alive per page.

That assumption breaks the moment two tabs holding the *same* route are open
at once — which "background tabs stay alive" makes routine, not exotic (e.g.
two different servers' `/monitoring` pages, or two live-progress pages with a
`[data-stream-sse-url]`). Both tabs' server-rendered HTML/data is correct, but
only **one** pane's *live* JS behavior (SSE stream hookup, live-metrics
WebSocket, auto-refresh countdown, etc.) is wired at a time — specifically
whichever such pane was created/activated most recently. This is a **known,
deliberate v0.6.0 scope boundary**, not an oversight or a bug to be quietly
worked around:

- It never corrupts data and never crashes anything. The underlying
  collection/job keeps running server-side regardless of which tab's JS is
  "wired."
- A tab's own **Reload** action (`NodexiaTabs.reload(id)`) always re-fetches
  fresh state for that pane, independent of which pane's live JS happens to
  be active.
- **Terminal is the one exception**, because CLAUDE.md requires true
  concurrent independent terminal sessions. `terminal.js` was given a small,
  targeted rewrite (see below) so each terminal pane is fully independent —
  two terminal tabs can be typed in, left running, and switched between
  freely with no cross-talk, no reconnect, and no new ticket consumed on
  switch.
- Every other module is intentionally left as-is in v0.6.0. Adopting the same
  pattern for another module is the expected upgrade path for a future
  release — see the next section.

## Making a page script tab-aware

`terminal.js` is the reference implementation. The pattern is small and
mechanical:

1. At the top of the script, resolve a scope root instead of assuming
   `document`:

   ```js
   var scopeRoot = (document.currentScript && document.currentScript.closest &&
     document.currentScript.closest('.tab-pane')) || document;
   function byId(id) { return scopeRoot.querySelector('#' + id); }
   ```

   This works because every page script is (re-)appended fresh **inside its
   own `.tab-pane`** when opened as a tab (see Architecture, above) —
   `document.currentScript` during that fresh execution is the clone sitting
   inside that exact pane. On a direct, non-tab page load there is no
   `.tab-pane` ancestor, so `scopeRoot` falls back to `document` and the
   script behaves exactly as it did before v0.6.0 — no template changes are
   required for this fallback to work.

2. Replace every `document.getElementById(id)` (and any
   `document.querySelector(...)`) call site with `byId(id)` /
   `scopeRoot.querySelector(...)`. Duplicate DOM ids across panes (e.g.
   `id="terminal-card"` present in more than one `.tab-pane` at once) become
   harmless — every lookup is scoped to one specific pane's subtree, never
   the whole document.

3. If the script does per-frame or per-event work that's wasteful while its
   pane is hidden (resize/measurement loops, polling), add an `active` flag
   gated by two new lifecycle events dispatched on `document`:
   `tab-activated` (`detail.pane` is the exact `.tab-pane` element — compare
   with `===` or `.contains()`) and `tab-deactivated`. Long-lived
   connections (a WebSocket, an SSE `EventSource`, a heartbeat timer) should
   generally **not** be paused this way — they should keep running in the
   background so no data is lost while a tab isn't in the foreground; only
   dispose them on `tab-closing`.
4. Expose a small control surface on the pane's root element (terminal does
   `card.__nodexiaTerminal = { pause, resume, dispose, isConnected }`) so a
   thin per-module "tab adapter" script — modeled on
   `terminal-tab-adapter.js` — can bridge `tab-activated` /
   `tab-deactivated` / `tab-closing` into that surface without the page
   script and the tab system ever referencing each other's internals
   directly; they only ever communicate through that exposed object and
   `CustomEvent`s on `document`.

See `web/static/terminal.js` and `web/static/terminal-tab-adapter.js` for the
concrete, working example end to end.

## Split view

Split view (side-by-side panes) is a **stub only in v0.6.0** — it does not
ship. What exists today is purely reserved scaffolding for a future release:
an empty, `hidden` `#tab-split-root` container, an unused CSS class scaffold
in `tabs.css`, and a documented-but-never-dispatched event name
(`tab-split-requested`). None of it is wired up; no `NodexiaTabs` method for
splitting exists. Treat any of that as a placeholder, not a partially-working
feature.

## Keyboard shortcuts

| Action | Real binding | Best-effort (may be blocked by the browser) |
|---|---|---|
| New tab | `Alt+T` | `Ctrl+T` |
| Close active tab | `Alt+W` | `Ctrl+W` |
| Next tab | `Alt+Shift+→` | `Ctrl+Tab`, `Ctrl+PageDown` |
| Previous tab | `Alt+Shift+←` | `Ctrl+Shift+Tab`, `Ctrl+PageUp` |
| Reopen last closed tab | `Alt+Shift+T` | `Ctrl+Shift+T` |
| Toggle pin on active tab | `Alt+P` | — |
| Duplicate active tab | `Alt+Shift+D` | — |
| Open tab switcher overlay | `Alt+Shift+A` | — |
| Jump to tab 1–8 | `Alt+1` … `Alt+8` | `Ctrl+1` … `Ctrl+8` |

`Ctrl+T`/`Ctrl+W` (and `Ctrl+Tab`, `Ctrl+Shift+Tab`, `Ctrl+PageUp/Down`,
`Ctrl+Shift+T`, `Ctrl+1..8`) could **not** be used as the primary bindings:
every major browser reserves these for its own native tab strip, and a
page's `keydown` handler either never sees them or has its
`preventDefault()` ignored by the browser's own tab-switching logic. Nodexia
still attempts them via a capture-phase listener — it's a real, no-cost
attempt that happens to work in one genuine context (Nodexia installed as a
standalone PWA window has no native browser tab strip to conflict with), but
that's a bonus, never something to rely on in a normal browser window.

All bindings are detected via `event.code` (the physical key, e.g. `KeyT`,
`ArrowLeft`), never `event.key` — on macOS, `Option`+letter often produces a
different Unicode character in `event.key` (e.g. `Option+T` → `†`), which
would make key-based matching unreliable cross-platform. Shortcuts are inert
while a text input/textarea/contenteditable has focus.

## Mobile behavior

Below `768px` viewport width, the tab bar is replaced by a horizontally
scrollable strip plus a full-screen bottom-sheet **tab switcher**, reachable
via a floating action button (FAB, opposite side from the existing
"back to top" button). New Tab / Close All live in the switcher sheet's
header; each card in the sheet has its own close button and activates +
dismisses the sheet on tap.

- **Breakpoints:** mobile `< 768px`, tablet `768–1024px`, desktop `> 1024px`
  (new, specific to the tab system — distinct from `style.css`'s existing
  ad-hoc breakpoints elsewhere in the app).
- **Gestures:** swiping the tab bar scrolls it natively (no custom JS).
  Swiping the content area left/right switches tabs (vertical drags are left
  alone so page scrolling is never hijacked). The mobile tab switcher sheet
  can also be dismissed with a downward swipe. Long-pressing a tab (500 ms)
  opens the same context menu as desktop right-click; long-pressing a link
  offers an "Open" / "Open in new tab" action sheet. Both the context menu
  and action sheet are keyboard-navigable. Pull-to-refresh is disabled on
  pane content so an accidental vertical drag can't trigger a full reload.
- **5-tab mobile cap:** `NodexiaTabs.MOBILE_TAB_LIMIT` is `5`, enforced only
  below the `768px` breakpoint, inside `NodexiaTabs.open()`. Opening a 6th
  tab evicts the oldest non-pinned, non-active background tab first; if all
  5 are pinned and/or active, the new tab is refused and a toast is shown
  instead. There is no tab-count cap on tablet or desktop widths.
- Backgrounding the **browser tab itself** (not an internal tab) — e.g.
  switching apps — fires `tab-deactivated`/`tab-activated` for the currently
  active internal tab around the `visibilitychange` boundary, on top of the
  normal per-tab-switch events. This does not close WebSockets or drop the
  terminal's heartbeat.

## Release note

This documentation and the tab system implementation do **not** bump the
app's version string. That is a separate, manual step for a maintainer:
running `make release VERSION=v0.6.2` (the existing `scripts/release.sh`)
once the change set has been reviewed and `make test` passes. See
`CHANGELOG.md` for the corresponding entry.
