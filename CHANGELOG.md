<!-- Introduced in v0.6.0 (multi-tab workspace). -->

# Changelog

All notable changes to Nodexia are documented in this file. The format
loosely follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/);
this project does not follow strict SemVer pre-1.0, but version tags are
still `vMAJOR.MINOR.PATCH`.

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
