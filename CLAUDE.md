# CLAUDE.md

Guidance for Claude Code working in the Nodexia repository.

## Product

Self-hosted control panel for monitoring and managing **Rebecca & Pasarguard**
nodes. Priorities, in order: (1) traffic monitoring, (2) resource monitoring,
(3) node discovery/management, (4) server registry, (5) background collection.

SSH commands, SFTP, bulk actions, and the in-browser terminal are **supporting
tools** — do not expand them without being asked.

## Hard constraints

- **Go 1.25, stdlib HTTP only** (`net/http` + `ServeMux`). No web framework.
- **SSR with `html/template`** — no SPA / JS framework / client rendering. Keep
  `web/static` minimal.
- **Few dependencies** (sftp, mysql, modernc sqlite, x/crypto, coder/websocket —
  pinned). Justify any new module before adding it.
- **Portable SQL** (SQLite now, MySQL planned): explicit column lists, `?`
  placeholders, `LastInsertId()` not `RETURNING`, and go through `db.DBTX` in
  shared paths.
- **Git**: work on `main` only; push to `origin main`; author every commit as
  **Ho3einK84** (`ho3ein.cyber@yahoo.com`). Conventional Commits
  (`type(scope): subject`, lowercase, imperative, ≤ ~50 chars).

## Layout

- `cmd/nodexia/main.go` — entrypoint
- `internal/app/` — bootstrap + module wiring
- `internal/config/` — env config (`NODEXIA_*`)
- `internal/db/` — drivers, migrations, `DBTX`
- `internal/http/` — router, handlers, middleware
- `internal/module/` — feature modules + `registry`
- `internal/scheduler/` — background jobs (monitoring/nodes sweeps, analytics
  rollup/cleanup, alert evaluation, country resolution, status digest)
- `internal/sshclient/` — shared SSH runtime
- `internal/view/` — `Renderer`, `PageData`, nav, client-i18n bridge
- `internal/i18n/` — en/fa catalogs (`locales/*.json`) + lookup; parity-tested
- `internal/notify/` — Telegram client + alert/digest message templates
- `internal/backup/` — encrypted logical DB backup/restore (diagnostics page)
- `internal/geoip/`, `internal/livemetrics/` — country lookup, live SSE metrics
- `web/templates/`, `web/static/` — SSR templates + assets
- `schema.sql` — schema + migration bookkeeping (append new statements only)
- Plus `commandstream/`, `terminalticket/`, `ratelimit/`.

## Patterns (mirror `servers` and `nodes`)

**Module** (`module.Module`): `Name`, `RouteGroup`, `RegisterRoutes(mux, deps)`;
register in `registry/registry.go` → `DefaultModules()`. `RegisterRoutes` must
fall back to `module.NewPlaceholderHandler` when DB/SSH are unavailable — never
panic. Routes are server-scoped `/servers/{id}/<group>`; read id via
`r.PathValue("id")`.

**Repository**: domain type + `Repository` interface in `repository.go`;
`SQLRepository` over `*sql.DB` in `sql_repository.go`. Wrap multi-statement
writes in `BeginTx`/`Rollback`/`Commit`. Wrap errors
`fmt.Errorf("pkg: op: %w", err)`. Export `ErrNotFound`, map `sql.ErrNoRows`.

## HTTP & security

- Auth: single admin; two HMAC cookies (`nodexia_session` = CSRF anchor,
  `nodexia_auth` = identity), both HttpOnly + SameSite=Lax.
- Every form embeds CSRF: hidden `_csrf_token` from
  `middleware.GetCSRFToken(r.Context())`.
- Render via `deps.Renderer.Render(...)` with a `view.PageData` (set
  `ContentTemplate`, `Title`, `ActiveNav`, …). Reuse existing middleware.
- SSH credentials are **runtime-only** — the DB stores only strategy + reference
  metadata. Use `sshclient.Service`; preserve trust-on-first-use host-key pinning.
- **Bilingual (en/fa + RTL)**: user-facing strings live in `internal/i18n/locales`
  — add keys to **both** `en.json` and `fa.json` (parity is enforced by
  `i18n/parity_test.go`). Resolve in handlers via `page.T("key", args…)`; client
  JS strings use the `js.*` namespace. Telegram alert/digest text is template
  English only (the notify layer has no request locale).

## Module invariants (don't regress)

- **bulk**: POST never runs SSH inline — it creates an in-memory job and
  redirects to a 2 s auto-refresh page. 5-worker pool. Own timeouts (reboot 2m,
  update 20m), not the 20 s SSH default. Sudo preamble exit codes: 88 = needs
  password, 87 = no package manager. Servers without credentials are skipped,
  never dropped.
- **nodes**: provider/driver arch (`DefaultProviders()`); never hardcode node
  names — they come from remote config and must pass `ValidateNodeName`. Actions
  and installs run as background `commandstream`/job sessions. The PasarGuard
  install drives the official script over **positional stdin** (fragile —
  re-count prompts if it breaks) then patches `/opt/<name>/.env`; generated shell
  must stay single-quote-free inside `sh -c '...'` (`TestGeneratedShellSyntax`
  guard). Discovery + Docker actions use `sudo -n`. One discovery sweep = one
  `created_at`. API key/cert are shown once, never persisted. Rebecca is
  detect/manage only.
- **terminal**: single-use tickets (30 s TTL, max 3/user) passed via
  `data-ticket`. WS JSON frames (`input`/`resize` → `output`/`error`), 5 s write
  deadline, same-origin checked before accept. Any `ResponseWriter` wrapper must
  implement `Hijacker` + `Flusher` + `Unwrap()` or the upgrade breaks. xterm
  v5.5.0 + the `@xterm/addon-*` suite (fit, unicode11, web-links, search,
  serialize, webgl, canvas) are vendored in `web/static` (`script-src 'self'` —
  no CDN). `xterm-themes.js` (theme catalog) and `terminal-keybindings.js`
  (shortcut handler) load before `terminal.js`. Renderer is WebGL on desktop
  (canvas fallback), canvas on mobile. Server sends a 30 s WS ping keepalive plus
  `status`/`heartbeat` control frames; the single-use ticket model is unchanged,
  so reconnect re-enters the credential page rather than re-dialing.
- **commands**: `run`/`stream` start background sessions (never inline); `test`
  is a sync connection check; interactive TUIs (top/vim/mysql/`tail -f`…) are
  detected server-side and redirected to the terminal
  (`data-interactive-programs` is the shared source of truth).
- **monitoring**: each sweep stores ONE latest vnStat snapshot per server (JSON
  daily/monthly rows). Daily rows are tailed to **35** (`retainedDailyRows`, 5
  weeks) so the seasonal forecast has ≥ a few samples per weekday; monthly rows to
  6. `TotalBytes` always defaults to `RX+TX`. Forecast/analytics work on **download
  (RX)** only.
- **analytics/forecast**: `ForecastService.Compute(days, months, limitBytes)`
  selects an algorithm by history length (trend < 7 ≤ MA < 14 ≤ WMA < 21 ≤
  seasonal). "This month" uses the authoritative monthly RX row, not a daily sum
  (daily history can straddle a month boundary). `computeExhaustion` is bounded to
  month-end (a monthly cap resets there) and reuses the SAME per-day predictor as
  the month projection so they can't disagree. The optional per-server monthly cap
  lives in its own `server_traffic_limits` table (RX-only); 0/no row = unlimited.
- **alerts**: the evaluator persists events, cooldowns, and consecutive-hit
  streaks. A metric that is unavailable this cycle is **skipped** (neither fired
  nor resolved) to keep streaks/cooldowns sane across gaps; recovery resolves an
  open event even while silenced. **Predictive metrics** (`projected_exceed_limit`
  boolean ≥ 1, `days_to_exhaustion` ≤ N) are forecast-derived, gated on
  `ForecastAvailable` (limit configured + enough history), and reuse the analytics
  forecast. `DaysToExhaustionSafe` (a large sentinel) is reported when safe so a
  "≤ N days" rule can resolve instead of getting stuck. Predictive comparators/
  thresholds are normalised in validation — don't let the operator store an
  incoherent condition.
- **digest** (scheduler): opt-in periodic Telegram summary
  (`NODEXIA_DIGEST_*`), own ticker, runs only when enabled AND a notifier exists;
  first send is one interval after startup, never immediately. Best-effort — every
  failure is logged and swallowed, never disrupts the scheduler.

## Workflow

- Tests live beside code (`*_test.go`), helpers in `internal/testutil`. Add tests
  for new logic (parsing, validation, SQL repos).
- Run `go vet ./...` and `make test` after changes.
- Mirror an existing module's files for a new feature instead of inventing
  structure. Prefer small, surgical changes.

```bash
make build              # → bin/nodexia
make test               # go test ./...
go run ./cmd/nodexia/   # local dev (needs NODEXIA_AUTH_USERNAME/PASSWORD)
go vet ./...
```
