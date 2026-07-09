<!-- Introduced in v0.6.0 (multi-tab workspace). -->

# Changelog

All notable changes to Nodexia are documented in this file. The format
loosely follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/);
this project does not follow strict SemVer pre-1.0, but version tags are
still `vMAJOR.MINOR.PATCH`.

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
