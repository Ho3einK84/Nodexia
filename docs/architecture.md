# Nodexia Architecture

> High-level system architecture, component relationships, and engineering standards for Nodexia.
> For frontend specifics, see [`FRONTEND.md`](../FRONTEND.md); for tab mechanics, see [`docs/tab-system.md`](tab-system.md).

---

## 1. Stack at a Glance

- **Backend Runtime:** Go 1.26+ with standard library HTTP (`net/http`, Go 1.22+ routing). No web framework.
- **Frontend Architecture:** **Zero-Build** (no npm, vite, or bundlers).
  - **HTML:** Server-side Go `html/template` composed from layout and partials (`web/templates/partials/`).
  - **JavaScript:** Browser-native ES Modules (`app.js` orchestrator + `web/static/modules/`).
  - **CSS:** Native CSS design tokens (`tokens.css`), application shell chrome (`shell.css`), core UI library (`style.css`), and tab styles (`tabs.css`). Full bidirectional (LTR/RTL) support.
- **Asset Embedding:** Templates and static files are compiled into the binary via `go:embed` with SHA-256 cache-busting fingerprints.
- **Database:** Pure Go SQLite (`modernc.org/sqlite`) by default, with MySQL 8+ support via `go-sql-driver/mysql`. Queries use the shared `db.DBTX` interface.
- **Remote Operations:** SSH (`golang.org/x/crypto/ssh`) with TOFU host-key pinning, SFTP (`pkg/sftp`), and in-browser WebSocket terminal (`coder/websocket` + vendored xterm.js).

---

## 2. Repository Layout

```
Nodexia/
├── cmd/nodexia/          # Main application entrypoint
├── internal/
│   ├── app/              # Bootstrap, dependency wiring, and shutdown
│   ├── config/           # Environment configuration (NODEXIA_*)
│   ├── db/               # Drivers, migrations, and shared DBTX
│   ├── http/             # Router, middleware chain, and shared handlers
│   ├── module/           # Feature domain modules
│   │   ├── servers/      # Server inventory and credentials
│   │   ├── nodes/        # Rebecca & PasarGuard discovery and deployment
│   │   ├── monitoring/   # Telemetry collection sweeps (CPU, RAM, vnStat)
│   │   ├── analytics/    # Forecasting models and traffic quota math
│   │   ├── alerts/       # Rule evaluation, cooldowns, and Telegram delivery
│   │   ├── terminal/     # Web terminal SSH sessions and ticket broker
│   │   ├── commands/     # Remote command execution and streaming
│   │   ├── files/        # SFTP file manager
│   │   ├── bulk/         # Asynchronous batch tasks (reboot, update)
│   │   └── system/       # Host diagnostics and SQLite backup/restore
│   ├── scheduler/        # Background tickers (sweeps, alerts, rollups)
│   ├── sshclient/        # Shared SSH connection pool and runtime
│   ├── view/             # HTML render engine, PageData models, i18n bridge
│   ├── i18n/             # En/Fa translation catalogs (en.json, fa.json)
│   └── notify/           # Telegram client and alert formatters
├── web/
│   ├── templates/        # Go html/templates (layout, partials, views)
│   └── static/           # Static assets (tokens.css, shell.css, JS modules)
├── schema.sql            # Append-only database migrations
├── FRONTEND.md           # Authoritative frontend & UI guidelines
└── assets.go             # Asset embedding and fingerprint registry
```

---

## 3. Request Lifecycle & Middleware

Incoming requests pass through a layered middleware pipeline:

```
Request ──► RequestID ──► SecurityHeaders ──► Session ──► Locale ──► RequireAuth ──► CSRF ──► Logging ──► Compress ──► Recover ──► Module Handler
```

1. **Routing:** `internal/http/router.go` mounts domain modules to the `ServeMux`.
2. **Authentication:** Two HTTP-only, `SameSite=Lax` cookies:
   - `nodexia_session`: visitor session anchoring the CSRF token.
   - `nodexia_auth`: authenticated admin identity with expiration.
3. **CSRF Protection:** Synchronizer token pattern checking same-origin headers and hidden `_csrf_token` (or `X-CSRF-Token`) on mutable requests. `RequireAuth` runs before `CSRF`, and every response carries a fresh `X-CSRF-Token` header so AJAX actions need no pre-flight token request.
4. **Compression:** `middleware.Compress` gzips text responses through a `sync.Pool` and skips SSE, WebSocket, Range, and already-compressed content.
5. **Rendering:** Handlers call `deps.Renderer.Render(w, status, view.PageData)` to execute the layout template with server-side localization (`page.T("key")`). Requests carrying `X-Nodexia-Partial: 1` (tab navigation) receive the page body without the shell, and ES modules are served immutably under `/static/v/<digest>/...`.

---

## 4. Module & Repository Pattern

All feature modules implement the `module.Module` contract:

```go
type Module interface {
    Name() string
    RouteGroup() string
    RegisterRoutes(mux *http.ServeMux, deps module.Dependencies)
}
```

- **Database Access:** Modules define a domain entity, a `Repository` interface, and an SQL implementation over `db.DBTX`.
- **Transactions:** Multi-statement writes execute inside explicit transactions (`BeginTx`/`Commit`/`Rollback`).
- **Sentinel Errors:** Map driver `sql.ErrNoRows` to `ErrNotFound` at the repository boundary.

---

## 5. Key Subsystems

### 5.1. Multi-Tab Workspace
The workspace (`tab-manager.js`) intercepts in-app navigation and dynamically loads server-rendered pages into isolated `.tab-pane` containers via `fetch()` and `DOMParser`. Background tabs stay alive without reloading, preserving live state (e.g., active SSH terminals).

### 5.2. Web Terminal WebSocket Lifecycle
1. `GET /servers/{id}/terminal`: Renders credentials form.
2. `POST /servers/{id}/terminal`: Verifies SSH credentials and issues a 30-second single-use ticket.
3. `GET /servers/{id}/terminal/ws?ticket=...`: Upgrades connection via `coder/websocket`, opens a PTY shell (`xterm-256color`), and pipes bidirectional I/O.

### 5.3. SSH/SFTP Connection Pool & Database Read Pool
`internal/sshclient/pool.go` reuses SSH and SFTP connections (90s idle expiry, 30s keepalive) and caches `known_hosts` in memory. GET pages for files, monitoring, and system never trigger SSH collection inline. SQLite uses a separate WAL read pool (`Runtime.Reader()`) alongside the single writer; MySQL is unaffected.

### 5.4. Scheduler & Background Workers
`internal/scheduler/` manages asynchronous cron tasks:
- **Telemetry Sweeps:** Resource & vnStat snapshots.
- **Node Discovery:** Background checks for Rebecca/PasarGuard processes.
- **Analytics Rollups:** Compaction of daily/monthly traffic samples.
- **Alert Evaluation:** Rule thresholds, predictive forecasting triggers, and Telegram dispatch.

---

## 6. Critical Module Invariants

To avoid regressions, maintain these key domain guarantees:

- **Bulk Actions:** Never run SSH inline; queue jobs in an in-memory worker pool and redirect to an auto-refreshing status view.
- **Node Management:** Discovery sweeps execute under a per-server single-flight lock (`AcquireServerSweep`), replace `node_snapshots` atomically per `sweep_id` only on healthy/empty sweeps (preserving cached nodes when SSH fails), keep `GET /servers/{id}/nodes` strictly read-only, and maintain 30-day UTC daily uptime buckets in `node_status_history`. Installs allocate a non-echoing PTY with base64 stdin secret framing (`umask 077`) for Rebecca, preflight PasarGuard script prompts before execution, and gate destructive `--override` flags behind explicit `confirm_override` confirmation.
- **Terminal:** Static assets for xterm.js must remain vendored (`script-src 'self'`); WebGL renderer with canvas fallback.
- **Analytics & Quotas:** Daily vnStat rows are retained for 35 days (5 weeks) to power seasonal weekday forecasting; period resets anchor to `traffic_reset_day` and the fleet, per-server, Overview, and Alerts views share the same billing-period math. `GET /servers/{id}/analytics/data?metrics=...` aggregates chart series in one request (with 7D/30D fallback for servers younger than the range), and `GET/POST /analytics/limits*` manages global and per-tag traffic limits via AJAX JSON. Charts are server-data-driven SVG with no build-time dependencies.
- **Security Posture:** Production refuses session secrets shorter than 32 characters; client IP is taken from the right-most trusted `X-Forwarded-For` hop with IPv6 grouped by `/64`; terminal sessions are closed on logout, delete, and shutdown; `/healthz/ready` never leaks error details.
- **Alerts:** Unavailable metrics during a sweep are skipped (neither fired nor resolved) to prevent false cooldown flappings.
- **Bilingual Parity:** All new UI text must be defined in both `en.json` and `fa.json`. Key parity is enforced by `internal/i18n/parity_test.go`.

---

## 7. Development & Verification

```bash
make test             # Run complete unit, race, and parity test suite
go vet ./...          # Static code inspection
make build            # Compile binary to bin/nodexia
go run ./cmd/nodexia/ # Launch local dev server (port 8080)
```
