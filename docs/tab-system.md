# Multi-Tab Workspace

> Architecture, lifecycle, and developer guide for Nodexia's browser-tab workspace.

---

## 1. Overview

The multi-tab workspace allows users to open and switch between several views (servers, terminal sessions, metrics, alerts) concurrently without full page reloads.

- **Desktop/Tablet:** Top horizontal tab strip with pinning, context menu, and split-view hooks.
- **Mobile (< 768px):** Bottom-sheet tab switcher accessed via a Floating Action Button (FAB).

---

## 2. Architecture & Render Path

Tabs use native DOM containers (`<div class="tab-pane">`) rather than `<iframe>` (preserving strict `frame-ancestors 'none'` CSP).

1. **Server-Side Render Parity:** Opening a tab triggers a standard `fetch(url)`. The server renders the full page identically to a direct browser visit.
2. **DOM Extraction:** `tab-manager.js` parses the response with `DOMParser`, extracts `#tab-content`, and mounts it into a fresh `.tab-pane`.
3. **Asset Deduplication & Execution:**
   - Stylesheets (`<link rel="stylesheet">`) in `<head>` are deduplicated and appended globally once.
   - Page-specific scripts (`PageScripts`) are cloned and appended directly **inside the new `.tab-pane`** to preserve pane-scoped DOM execution.
4. **Persistence:** Tab metadata (URL, title, icon, pinned state) is stored in `sessionStorage` to restore tabs on page refresh.

---

## 3. Tab Lifecycle & Scoping

To allow multiple tabs of the same route (or concurrent terminal sessions) without ID collision:

### 3.1. Pane Scoping
Page scripts should resolve elements relative to their tab container:

```javascript
const scopeRoot = (document.currentScript && document.currentScript.closest('.tab-pane')) || document;
const byId = (id) => scopeRoot.querySelector(`#${id}`);
```

### 3.2. Lifecycle Events
`tab-manager.js` dispatches custom events on `document`:
- `tab-activated`: Tab entered foreground (`event.detail.pane`).
- `tab-deactivated`: Tab backgrounded.
- `tab-closing`: Tab destroyed; use this to close WebSockets, cancel timers, or disconnect streams.

---

## 4. Navigation & Gestures

| Gesture | Action |
|---|---|
| Plain Click / Tap | Navigates inside the active tab |
| `Ctrl`/`Cmd` + Click | Opens link in a new background tab |
| Middle Click | Opens link in a new background tab |
| Mobile Long-Press | Context menu ("Open", "Open in new tab") |

---

## 5. Keyboard Shortcuts

| Action | Primary Shortcut | Fallback / PWA |
|---|---|---|
| New tab | `Alt+T` | `Ctrl+T` |
| Close active tab | `Alt+W` | `Ctrl+W` |
| Next tab | `Alt+Shift+→` | `Ctrl+Tab`, `Ctrl+PageDown` |
| Previous tab | `Alt+Shift+←` | `Ctrl+Shift+Tab`, `Ctrl+PageUp` |
| Reopen closed tab | `Alt+Shift+T` | `Ctrl+Shift+T` |
| Pin/Unpin tab | `Alt+P` | — |
| Tab switcher | `Alt+Shift+A` | — |
| Jump to tab 1–8 | `Alt+1` … `Alt+8` | `Ctrl+1` … `Ctrl+8` |

> Shortcuts use physical `event.code` to guarantee consistent cross-platform behavior across keyboard layouts.

---

## 6. Mobile Adaptations (< 768px)

- **Cap:** Max 5 open tabs on mobile (opening a 6th evicts the oldest inactive unpinned tab).
- **Gestures:** Horizontal content swipe switches active tabs; downward swipe dismisses the tab switcher sheet.
- **Backgrounding:** App visibility changes trigger `tab-deactivated`/`tab-activated` without disconnecting active WebSockets.
