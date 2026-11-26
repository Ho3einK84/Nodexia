# Nodexia Frontend Architecture (`FRONTEND.md`)

Concise reference for engineers and AI agents modifying the Nodexia UI.

## 1. Non-Negotiable Constraints
- **Zero-Build:** No npm, bundlers, Tailwind, or CSS preprocessors. Everything runs natively (Go `html/template`, pure CSS, native ES modules) and is embedded via `embed.FS` in `assets.go`.
- **Strict CSP:** No inline `<script>`, no inline event handlers (`onclick=`), no `unsafe-eval`. Client i18n is emitted as `<script type="application/json" id="nodexia-i18n">`.
- **Bidirectional (LTR `en` + RTL `fa`):** Always use CSS logical properties (`margin-inline-*`, `padding-inline-*`, `inset-inline-*`, `border-inline-*`, `text-align: start`). Keep technical strings (`code`, `pre`, IPs, ports, paths) isolated in `dir="ltr"`.
- **60fps Smoothness Guardrails:**
  - Never use `backdrop-filter` or `transition: all` in stylesheets.
  - Never call unscoped `lucide.createIcons()` — always use `renderIcons(scope)` / `window.nxRenderIcons(scope)` (enforced by `TestNoUnscopedLucideCreateIcons`).
  - Batch bulk DOM updates via `DocumentFragment` and throttle scroll/resize/SSE streams via `requestAnimationFrame`.

## 2. CSS Cascade & Design System ("Dark Graphite Instrument")
Loaded in `web/templates/partials.gohtml` via `{{ asset "/static/..." }}` (content-hashed `?v=`):

| Layer | Files | Scope |
| :--- | :--- | :--- |
| **1. Tokens** | `tokens.css` | Surface/alpha/accent/semantic tokens (`--bg-canvas`, `--surface-*`, `--accent`, `--ok`/`--warn`/`--crit`/`--info`), typography (Inter, JetBrains Mono, Vazirmatn), resets, RTL rules. |
| **2. Chrome** | `shell.css`, `tabs.css` | Authenticated shell header, sidebar/drawer, bottom nav, multi-tab workspace & split view. *Skipped on `/login`.* |
| **3. Primitives** | `style.css` | Shared `.page-hero`, `.kpi-strip`, `.card`, `.btn`, `.badge`, `.country-tag`, `.status-pill`, `.flash-banner`, `.modal`, `.action-menu`, `.nx-toast`, forms, tables, collapsibles. *Skipped on `/login`.* |
| **4. Isolated Auth** | `login.css` | Self-contained `/login` stylesheet over `tokens.css` only. |
| **5. Page Modules** | Via `.PageStyles`: `home.css`, `servers.css`, `diagnostics.css`, `nodes.css`, `monitoring.css`, `analytics.css`, `alerts.css`, `system.css`, `commands.css`, `files.css`, `bulk.css`, `terminal.css` | Loaded only when their route/tab is open (`tab-manager.js` hoists and awaits new `<link>` tags). |

**Standard Breakpoints (strictly 3):**
- `<= 1024px`: Tablet / compact desktop
- `<= 767px`: Mobile layout (bottom nav, action bottom-sheets, stacked tables)
- `<= 480px`: Narrow phone

## 3. Go Templates & Shared Partials (`web/templates/`)
- `layout.gohtml` + `partials.gohtml`: Root document, shell chrome, `server-header` (per-server workspace header & sub-nav).
- `ui_partials.gohtml`: Shared cross-page partials:
  - `flash-banner`: Unified status feedback banner.
  - `country-tag`: Monospace ISO 3166-1 alpha-2 badge (`countryCode` func in `internal/view/render.go`).
  - `page-hero-head`: Unified page title + optional info popover (`?`) + actions slot.
  - `ssh-advanced-fields`: Shared SSH override/timeout fields (`dict` helper).
  - `pagination`: Shared server-rendered pager.
- Keep templates concise; split large views into `[feature]_[section].gohtml` partials.

## 4. JavaScript Architecture (`web/static/`)

### Core ES Modules (always loaded via `app.js` on authenticated pages)
| File | Role |
| :--- | :--- |
| `app.js` | Thin boot orchestrator & tab-pane rescan entry (`window.nxRescan`). Lazy-imports domain modules only when their DOM hooks exist. |
| `modules/core.js` | Client i18n, byte formatting, escaping, CSRF, DOM factory, pane scoping, unified `.nx-toast` stack, strictly scoped `renderIcons`, top bar, delayed `#loading-overlay` (180ms), cleanup registry. |
| `modules/ui.js` | Form guards, flash dismiss, masked secrets, copy buttons/targets, GPU `--pct` progress bars, rAF back-to-top & page-info popovers, collapsibles, keyboard shortcuts. |
| `modules/modals.js` | Modal open/close with focus trap & scroll lock, accessible card overflow `.action-menu`, and the unified Dark Graphite confirm dialog `window.nxConfirm()` (never use blocking `window.confirm()`). |
| `modules/shell.js` | Dismissible fleet warnings, mobile drawer, bottom nav, PWA SW registration, portrait lock. |

### Lazy ES Modules (loaded via dynamic `import()` on demand)
| File | Loaded When |
| :--- | :--- |
| `modules/stream.js` | `[data-stream-sse-url]`, `[data-auto-refresh]`, or `[data-node-result]` exists in pane. |
| `modules/log-drawer.js` | `[data-log-drawer]` exists (rAF + `DocumentFragment` SSE log streaming). |
| `modules/nodes.js` | `[data-nodes-workspace]` or `[data-node-credentials]` exists. |
| `modules/nodes-deploy.js` | User opens the Node Deploy modal (`[data-modal-open="deploy-node-modal"]` / `[data-engine-preselect]`). |

### Classic Page Scripts & `window.nx*` Bridge
Classic scripts (`tab-manager.js`, `files.js`, `analytics.js`, `diagnostics.js`, `alerts.js`, `commands.js`, `bulk.js`, `monitoring.js`, `livemetrics.js`, `terminal.js`) run per `.tab-pane` (scoped via `document.currentScript.closest('.tab-pane')` and cleaned up via `pane.__nxCleanups`) and consume shared primitives exposed by `core.js` / `modals.js`:

- `window.nxT(key, params)` / `window.nxTn(key, count, params)` — i18n lookup
- `window.nxHumanBytes(n)` — human-readable byte sizes
- `window.nxEscape(str)` — HTML entity escaping
- `window.nxCSRF(root)` — CSRF token lookup
- `window.nxEl(tag, attrs, children)` — declarative DOM element builder
- `window.nxPane(scriptEl)` — returns `{ pane, root, isScoped, byId, onCleanup }`
- `window.nxToast(msg, kind, durationMs)` — unified `.nx-toast-region` stack (`ok|warn|error|info`)
- `window.nxRenderIcons(scope)` — strictly scoped Lucide icon rendering
- `window.nxModal` — `{ open, close, syncScrollLock }`

`/login` loads only `tokens.css`, `login.css`, `lucide.min.js`, and `login.js`.

## 5. Verification Checklist
Run after every UI change:
```bash
go test ./...
make build
node --check web/static/*.js web/static/modules/*.js
```
Automated tests in `assets_test.go` and `internal/view/` verify CSS brace balance, JS syntax via `node --check`, `sw.js` `PRECACHE_URLS` existence, zero unscoped `createIcons()`, template rendering in `en` & `fa` with no raw i18n keys, and interactive tab/terminal/collapsible contracts.
