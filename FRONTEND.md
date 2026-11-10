# Nodexia Frontend Architecture & Developer Guide (`FRONTEND.md`)

Welcome to the **Nodexia** frontend architecture guide. This document serves as the single source of truth for human engineers and autonomous AI agents developing, refactoring, or extending the Nodexia control plane user interface.

---

## 1. Core Philosophy: Zero-Build Tooling

Nodexia adheres strictly to a **Zero-Build Architecture**:

* **No Node.js build pipelines:** No `npm`, `pnpm`, `yarn`, `vite`, `webpack`, `rollup`, `esbuild`, or `parcel`.
* **No CSS pre/post-processors:** No `Sass`, `Less`, `PostCSS`, or Tailwind build steps.
* **100% Native Web Standards:**
  * **HTML:** Go `html/template` parsed and executed at server startup.
  * **JavaScript:** Browser-native ES Modules (`type="module"`, `import` / `export`), modern ECMAScript standard.
  * **CSS:** Pure standard CSS with native CSS Custom Properties (Design Tokens) and CSS Logical Properties for bidirectional layout (LTR / RTL).
  * **Single Binary Distribution:** All templates (`web/templates/*.gohtml`) and static assets (`web/static/**`) are embedded directly into the Go application binary via Go `embed.FS` (`//go:embed`).

### Why Zero-Build?
1. **Instant, Zero-Friction Deployment:** A single static Linux binary (`bin/nodexia`) contains the entire control plane UI. Deployments take seconds without `node_modules` caching or container build overhead.
2. **Zero Supply-Chain Risk:** Eliminates thousands of transient npm dependencies, vulnerabilities, and package lifecycle hooks.
3. **Strict Content Security Policy (CSP):** Runs securely without `unsafe-inline` or `unsafe-eval`.
4. **Permanent Maintainability:** Code written against browser standards works reliably across years and browser versions without build tooling deprecation.

---

## 2. Directory Structure & Asset Map

```
Nodexia/
├── assets.go                      # Go embed.FS declaration, content-hash fingerprints
├── assets_test.go                 # Asset fingerprinting & syntax regression tests
├── cmd/nodexia/main.go            # Application entrypoint
├── internal/
│   ├── view/                      # Go view models, template helpers, render logic
│   └── ...
└── web/
    ├── templates/                 # Go html/template files & partials
    │   ├── layout.gohtml          # Root HTML document wrapper (<head>, <body>, shell)
    │   ├── partials.gohtml        # Global shell partials (header, nav, drawer, footer)
    │   ├── home.gohtml            # Fleet overview dashboard
    │   ├── nodes.gohtml           # Nodes management orchestrator template
    │   ├── nodes_card.gohtml      # Node item card partial
    │   ├── nodes_deploy_modal.gohtml # Multi-engine node deploy wizard modal
    │   ├── servers.gohtml         # Servers list and summary view
    │   ├── server_form.gohtml     # Server create/edit form
    │   ├── alerts.gohtml          # Alerts monitoring view
    │   ├── alert_rule_form.gohtml # Alert rule configuration modal
    │   ├── alert_channel_form.gohtml # Telegram/Webhook channel modal
    │   ├── analytics.gohtml       # Server analytics view
    │   ├── analytics_global.gohtml# Global fleet metrics partial
    │   ├── analytics_limits.gohtml# Traffic limits & quota partial
    │   ├── monitoring.gohtml      # Real-time resource monitoring
    │   ├── monitoring_traffic.gohtml # Live traffic telemetry partial
    │   ├── monitoring_workspace.gohtml # Historical telemetry partial
    │   ├── commands.gohtml        # SSH command execution workspace
    │   ├── files.gohtml           # SFTP browser & file editor
    │   ├── system.gohtml          # Host system diagnostics & OS info
    │   ├── terminal.gohtml        # Web SSH terminal
    │   └── login.gohtml           # Immersive authentication scene
    └── static/                    # Browser assets (embedded via fs.Sub)
        ├── tokens.css             # Layer 1: Typography, design tokens, resets, RTL
        ├── shell.css              # Layer 2: Shell chrome, header, drawer, bottom-nav
        ├── style.css              # Layer 3: UI component library (cards, buttons, forms)
        ├── tabs.css               # Layer 4: Multi-tab workspace system
        ├── nodes.css              # Layer 5: Module-specific styles (Nodes fleet)
        ├── terminal.css           # Layer 5: Module-specific styles (Web terminal)
        ├── alerts.css             # Layer 5: Module-specific styles (Alert rules)
        ├── analytics.css          # Layer 5: Module-specific styles (Traffic charts)
        ├── monitoring.css         # Layer 5: Module-specific styles (Gauges)
        ├── files.css              # Layer 5: Module-specific styles (File manager)
        ├── commands.css           # Layer 5: Module-specific styles (Command stream)
        ├── bulk.css               # Layer 5: Module-specific styles (Batch actions)
        ├── system.css             # Layer 5: Module-specific styles (OS inspection)
        ├── login.css              # Layer 5: Module-specific styles (Auth scene)
        ├── app.js                 # Orchestrator entrypoint (< 100 lines)
        ├── modules/               # Domain-driven ES modules
        │   ├── core.js            # i18n (nxT), byte format, tab integration, hooks
        │   ├── ui.js              # Forms, copy buttons, password toggles, shortcuts
        │   ├── modals.js          # Modal manager, action menus, deploy wizard
        │   ├── stream.js          # Live SSE streams, refresh timers, log stream
        │   └── shell.js           # Fleet alerts, drawer, mobile menu, PWA, lock
        ├── tab-manager.js         # Multi-tab client engine
        ├── sw.js                  # Stale-While-Revalidate service worker
        ├── offline.html           # Offline fallback page
        └── fonts/                 # Self-hosted Exo 2 & Vazirmatn variable fonts
```

---

## 3. Go Template Architecture

### 3.1. Layout Composition
Every standard page render starts from `layout.gohtml`:
```html
<!doctype html>
<html lang="{{ .Lang }}" dir="{{ .Dir }}">
{{ template "head" . }}
<body>
  <div id="shell" class="shell">
    {{ template "shell-header" . }}
    {{ template "shell-nav" . }}
    {{ template "tab-bar" . }}

    <main id="shell-main" class="shell-main">
      <div id="tab-content" class="tab-content" data-tab-content>
        {{ .MainHTML }}
      </div>
      <div id="tab-split-root" class="tab-split-root" data-tab-split-root hidden></div>
      <div id="loading-overlay" class="loading-overlay" style="display:none">...</div>
    </main>

    {{ template "footer" . }}
  </div>

  {{ template "mobile-drawer" . }}
  {{ template "bottom-nav" . }}
  {{ template "tab-switcher" . }}
  {{ template "tab-fab" . }}

  <script type="application/json" id="nodexia-i18n">{{ clientI18nJSON }}</script>
  <script type="module" src="{{ asset "/static/app.js" }}"></script>
  <script src="{{ asset "/static/tab-manager.js" }}"></script>
  {{ range .PageScripts }}<script src="{{ asset . }}"></script>{{ end }}
</body>
</html>
```

### 3.2. Partial Naming & Slicing Conventions
* **Single Responsibility:** When a template file exceeds ~300 lines, extract standalone sub-sections into partials named `[feature]_[section].gohtml`.
* **Definition Tags:** Each partial begins with `{{ define "[name]" }}` and ends with `{{ end }}`.
* **Context Passing:** Always pass the required context explicitly: `{{ template "nodes-deploy-modal" . }}`.

### 3.3. Asset Helpers & Cache Busting
* Always link static files using `{{ asset "/static/filename.ext" }}`.
* The `asset` template function calculates a content-derived 6-character SHA-256 fingerprint (e.g. `/static/style.css?v=3a8f1b`).
* Hashes are computed once in memory on binary startup. When an asset's bytes change, its fingerprint changes automatically, busting both browser cache and Service Worker caches without manual version bumps.

### 3.4. The Client i18n JSON Island
To comply with strict CSP (which prohibits inline scripts), client-side translations are emitted as a non-executable JSON script block:
```html
<script type="application/json" id="nodexia-i18n">{{ clientI18nJSON }}</script>
```
`core.js` parses this island on boot and provides `window.nxT(key, params)` and `window.nxTn(key, count, params)` for localized dynamic strings.

---

## 4. JavaScript Architecture: Native ES Modules

### 4.1. The Orchestrator (`app.js`)
`web/static/app.js` is a lean orchestrator (< 100 lines). It does not hold business logic; instead, it imports specialized domain modules, listens for `DOMContentLoaded` (and tab switches), and initializes them cleanly.

```javascript
import { initCore, onPaneMount } from './modules/core.js';
import { initUI } from './modules/ui.js';
import { initModals } from './modules/modals.js';
import { initStream } from './modules/stream.js';
import { initShell } from './modules/shell.js';

function bootNodexia(scope = document) {
  initCore(scope);
  initUI(scope);
  initModals(scope);
  initStream(scope);
  initShell(scope);
}

document.addEventListener('DOMContentLoaded', () => bootNodexia(document));
window.addEventListener('nodexia:tab-activated', (e) => onPaneMount(e.detail.pane));
```

### 4.2. Domain Modules Breakdown

| Module | Location | Primary Responsibilities |
| :--- | :--- | :--- |
| **`core.js`** | `web/static/modules/core.js` | Client i18n (`nxT`, `nxTn`), formatBytes (`shortByteUnit`), multi-tab lifecycle integration, `registerPaneTeardown(fn)` hooks, Lucide re-scan triggers. |
| **`ui.js`** | `web/static/modules/ui.js` | Form auto-submits, button spinners, dynamic validation, flash banner dismissal, copy-to-clipboard buttons, password reveal toggles, keyboard shortcuts (`/` search, `Ctrl+K`), collapsible `<details class="card--collapsible">` state memory. |
| **`modals.js`** | `web/static/modules/modals.js` | Modal dialogs (`openModal`, `closeModal`), action overflow kebab menus (`.action-menu`), Node deployment wizard state machine (`initNodeDeployWizard`). |
| **`stream.js`** | `web/static/modules/stream.js` | Server-Sent Events (SSE) connections, automatic reconnection, live countdown pill for auto-refresh, real-time command streaming cards. |
| **`shell.js`** | `web/static/modules/shell.js` | Fleet-wide dismissible warning banners (`home-warnings`), server log drawer, mobile slide-in navigation drawer, bottom navigation active item state, PWA service worker registration, portrait lock management. |

### 4.3. Guide: Adding a New JavaScript Module
1. Create `web/static/modules/[name].js`.
2. Export an initializer function accepting an optional `scope` element:
   ```javascript
   export function initMyFeature(scope = document) {
     const elements = scope.querySelectorAll('[data-my-feature]');
     if (!elements.length) return;
     // Initialize features
   }
   ```
3. Register teardown hooks for tab switching:
   ```javascript
   import { registerPaneTeardown } from './core.js';
   // Clean up intervals, observers, or WebSockets when the tab unmounts
   registerPaneTeardown(pane, () => { /* cleanup */ });
   ```
4. Import and invoke in `web/static/app.js`.

---

## 5. CSS Architecture & Design Token System

Styles in Nodexia are layered in 5 distinct tiers. The loading sequence in `partials.gohtml` guarantees proper cascade inheritance:

```
Tier 1: tokens.css  ─►  Tier 2: shell.css  ─►  Tier 3: style.css  ─►  Tier 4: tabs.css  ─►  Tier 5: [module].css
```

### 5.1. Tier 1: `tokens.css` (Design Tokens & Normalization)
Defines the foundational design system via standard CSS custom properties:
* **Typography:** Self-hosted Exo 2 (400–800 variable) and Vazirmatn (5 weights).
* **Color System:**
  * Neutral Palette: `--slate-50` through `--slate-950`.
  * Primary Accent: `--accent` (`#3b82f6`), `--accent-soft` (`#60a5fa`), `--accent-bright` (`#93c5fd`).
  * Secondary Accent: `--violet` (`#8b5cf6`), `--violet-soft` (`#a78bfa`).
  * Semantic Status: `--ok` (`#4ade80`), `--warn` (`#facc15`), `--crit` (`#f87171`), `--info` (`#38bdf8`) with corresponding soft and background variations.
* **Surfaces:** `--bg-canvas` (`#0f172a`), `--bg-deep` (`#0b1120`), `--surface` (`rgba(15, 23, 42, 0.6)`), `--surface-2`, `--surface-elevated`.
* **Borders:** `--border` (`rgba(148, 163, 184, 0.1)`), `--border-strong` (`0.2`), `--border-subtle` (`0.06`).
* **Spacing Scale:** Standard 4px grid (`--space-0` to `--space-16`).
* **Border Radii:** `--radius-xs` (4px), `--radius-sm` (8px), `--radius-md` (12px), `--radius-lg` (16px), `--radius-xl` (20px), `--radius-full` (9999px).
* **Shadows & Elevation:** `--shadow-sm` through `--shadow-xl`, `--glow-accent`, `--glow-crit`, `--glow-ok`.
* **Z-Index Scale:** `--z-base` (1) to `--z-progress` (10000).
* **Global Resets & Reduced-Motion:** Normalizes box-sizing and honors `prefers-reduced-motion`.

### 5.2. Tier 2: `shell.css` (Persistent Application Chrome)
Styles everything surrounding the dynamic content:
* Main layout container: `.shell`, `.shell-header`, `.shell-subtitle`, `.meta`.
* Header controls: `.lang-switcher`, `.logout-link`, `.nav-icon`.
* Desktop primary navigation: `.shell-nav` with animated hover/active indicators.
* Loading & Progress: `.top-progress`, `.loading-overlay`, `.live-dot`, `.back-to-top`.
* Mobile Navigation: Hamburger `.nav-toggle`, slide-in `.drawer` and `.drawer-backdrop`, and fixed `.bottom-nav`.

### 5.3. Tier 3: `style.css` (Core UI Component Library)
Provides reusable component primitives used across all content pages:
* **Cards:** `.card`, `.card-header`, `.card-title`, `.card--collapsible`.
* **Server Items:** `.server-card`, `.server-item`, `.action-menu` (overflow kebabs).
* **Buttons:** `.btn`, `.btn--primary`, `.btn--secondary`, `.btn--danger`, `.btn--ghost`, busy states (`.is-busy`).
* **Forms:** `input`, `select`, `textarea`, `.form-group`, `.form-row`, secret reveal toggle.
* **Data Displays:** `.stats-row`, `.stat-card`, KPI metric rows, `.badge`, `.tag`, `.status-pill`.
* **Feedback:** `.flash-banner`, `.empty-state`, `.output-block` with copy buttons, streaming cards.
* **Modals & Tooltips:** `.modal`, `[data-tooltip]`.

### 5.4. Tier 4: `tabs.css` (Multi-Tab Workspace)
Controls the multi-tab workspace: desktop tab bar, scrollable tab list, pinned tabs, close actions, mobile tab switcher sheet, and mobile floating action button (FAB).

### 5.5. Tier 5: Module-Specific Styles
Stylesheets loaded only on specific pages via `.PageStyles`:
* `nodes.css`: Fleet overview cards, KPI strips, deployment wizards, node status badges.
* `terminal.css`: Xterm.js container, connection status, theme selector, terminal toolbar.
* `analytics.css`: Bandwidth cards, historical telemetry charts, data quota gauges.
* `monitoring.css`: Real-time CPU, RAM, disk, network telemetry.
* `files.css`, `commands.css`, `bulk.css`, `system.css`, `login.css`.

---

## 6. Internationalization & Bidirectional (RTL / LTR) Rules

Nodexia natively supports Persian (`fa`, RTL) and English (`en`, LTR).

### 6.1. Strict Rule: Use CSS Logical Properties
Never use physical directional properties in stylesheet rules:
* ❌ `margin-left`, `margin-right` ➔ ✅ `margin-inline-start`, `margin-inline-end`
* ❌ `padding-left`, `padding-right` ➔ ✅ `padding-inline-start`, `padding-inline-end`
* ❌ `border-left`, `border-right` ➔ ✅ `border-inline-start`, `border-inline-end`
* ❌ `left`, `right` (for offsets) ➔ ✅ `inset-inline-start`, `inset-inline-end`
* ❌ `text-align: left` ➔ ✅ `text-align: start`

### 6.2. Typography Swap
In `tokens.css`:
```css
html[lang="fa"] {
  --font-ui: 'Vazirmatn', 'Exo 2', ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif;
}
```

### 6.3. Numeric & Technical String Isolation
Technical strings (IP addresses, ports, units, shell snippets, gauges) must always read left-to-right, even in Persian mode:
```css
[dir="rtl"] code,
[dir="rtl"] pre,
[dir="rtl"] .metric-strip__value,
[dir="rtl"] .badge--load,
[dir="rtl"] .gauge__scale {
  direction: ltr;
  unicode-bidi: isolate;
}
```

---

## 7. Operational Guidelines for Future AI Coding Agents

When working on this codebase, AI coding assistants **must adhere to these inviolable rules**:

1. **Never Add Build Tools:** Do not introduce npm, yarn, Vite, Tailwind CLI, or any build step. All HTML, CSS, and JS must run directly in standard browsers.
2. **Preserve Strict CSP:** Never add inline `onclick=""`, inline `style=""` with dynamic logic, or inline `<script>` tags. Use `data-*` attributes and bind events in `web/static/modules/`.
3. **Always Maintain Test Suite Parity:**
   * Run `go test ./...` after any template or asset change.
   * Verify JavaScript syntax with Node: `node --check web/static/*.js web/static/modules/*.js`.
   * Verify CSS brace matching: all CSS files must maintain balanced braces.
4. **Service Worker Cache Bumping:** If you modify, add, or rename precached assets in `web/static/`, you **must** update `PRECACHE_URLS` and bump `CACHE_VERSION` in `web/static/sw.js`.
5. **No Regressions on Live Nodes:** Test builds using `go build ./cmd/nodexia` before deploying via `./scripts/deploy-de.sh`.
