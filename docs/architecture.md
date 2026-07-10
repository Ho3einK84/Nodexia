# Nodexia architecture

A high-level map of the codebase for new contributors. For the multi-tab
workspace, see [`tab-system.md`](tab-system.md).

## Stack at a glance

- **Language**: Go 1.25.
- **HTTP**: standard library only — `net/http` + `ServeMux` (Go 1.22+ pattern
  matching). No web framework, no router library.
- **Rendering**: server-side `html/template`. All pages are SSR; there is no
  client-side framework, no SPA, and no client rendering step. The
  `tab-manager.js` multi-tab workspace is the one place the browser does
  in-place navigation via `fetch()` + `DOMParser` + `importNode`, but the HTML
  it injects is still fully rendered server-side.
- **Asset embedding**: `web/templates/` and `web/static/` are compiled into
  the binary with `go:embed` in `assets.go`. A rebuild is required to ship a
  template or JS change — a container restart is not enough.
- **Database**: SQLite (via `modernc.org/sqlite`, pure Go) by default; MySQL
  is planned. All queries go through the `db.DBTX` interface in `internal/db/`
  so both drivers work behind the same code.
- **SSH**: `golang.org/x/crypto/ssh`. A shared client lives in
  `internal/sshclient/`. Host keys are pinned on first use.
- **SFTP**: `github.com/pkg/sftp`.
- **WebSocket terminal**: `github.com/coder/websocket`.
- **Pinned, minimal dependency set** — see `go.mod`. Justify any new module.

## Repository layout

```
cmd/nodexia/             entrypoint (main.go)
internal/app/            bootstrap + dependency wiring
internal/config/         env config (NODEXIA_*)
internal/db/             drivers, migrations, DBTX
internal/http/           router, middleware, shared handlers
internal/module/         feature modules + registry
  ├─ servers/            server registry CRUD
  ├─ nodes/              Rebecca / Pasarguard discovery + management
  ├─ monitoring/         resource + traffic sweeps
  ├─ analytics/          forecasting + limit math
  ├─ alerts/             rule evaluation + event history
  ├─ terminal/           in-browser SSH terminal
  ├─ commands/           run / stream / test
  ├─ files/              SFTP browser
  ├─ bulk/               multi-server actions
  ├─ system/             /ops pages
  └─ registry/           module registration (DefaultModules)
internal/scheduler/      background jobs (monitoring/nodes sweeps, analytics
                         rollup/cleanup, alert evaluation, country resolution,
                         status digest)
internal/sshclient/      shared SSH runtime
internal/view/           Renderer, PageData, nav, client-i18n bridge
internal/i18n/           en/fa catalogs (locales/*.json) + lookup; parity-tested
internal/notify/         Telegram client + alert/digest message templates
internal/backup/         encrypted logical DB backup/restore (diagnostics page)
internal/geoip/          country lookup for traffic sources
internal/livemetrics/    live SSE metrics
internal/terminalticket/ single-use WS ticket store (30s TTL)
internal/commandstream/  background SSH command sessions
internal/ratelimit/      login rate limiter
internal/sse/            Server-Sent Events helpers
schema.sql               schema + migration bookkeeping (append only)
web/templates/           html/template definitions
web/static/              JS, CSS, fonts, vendored xterm.js + addons
```

## Request lifecycle

1. `cmd/nodexia/main.go` calls `internal/app` to build the config, open the DB,
   start the SSH service, register the scheduler jobs, and construct the
   `http.Handler`.
2. `internal/http/router.go` mounts the module routes and wraps them in the
   middleware chain (outside-in):
   `Recover` → `Logging` → `RequireAuth` → `CSRF` → `Session` → `Locale` →
   `SecurityHeaders` → `RequestID`. Static assets, health, metrics, and PWA
   entry points are exempted where appropriate.
3. A module handler renders via `deps.Renderer.Render(w, status, view.PageData)`,
   which executes the layout + content template and writes the response.

## Module pattern

Every feature module implements `module.Module`:

```go
type Module interface {
    Name() string
    RouteGroup() string
    RegisterRoutes(mux *http.ServeMux, deps module.Dependencies)
}
```

Registration goes through `internal/module/registry/registry.go` →
`DefaultModules()`. **`RegisterRoutes` must fall back to
`module.NewPlaceholderHandler` when DB/SSH are unavailable — never panic.**
Routes are server-scoped `/servers/{id}/<group>`; read the id via
`r.PathValue("id")`.

### Repository pattern

Each module that touches the DB defines:

- A domain type in `types.go` (or similar).
- A `Repository` interface in `repository.go`.
- An `SQLRepository` over `*sql.DB` in `sql_repository.go`.

Multi-statement writes go in a transaction (`BeginTx`/`Commit`/`Rollback`).
Errors are wrapped with `fmt.Errorf("pkg: op: %w", err)`. Export `ErrNotFound`
and map `sql.ErrNoRows` to it at the repository boundary so handlers can use
a single sentinel.

## Multi-tab workspace (v0.6.0+)

The browser keeps a single page loaded and swaps in new content per tab. The
full design is in [`docs/tab-system.md`](tab-system.md); the short version:

- **Server-side** renders the full HTML page (layout + content + scripts +
  styles) for every route, as before. The tab bar markup is part of the layout.
- **Client-side** `tab-manager.js` (the only non-vendored `web/static` script
  that orchestrates navigation) intercepts in-tab links and form submits.
  Links → `pushState` + `fetch()` + `DOMParser` extraction of `#tab-content`
  into the active `.tab-pane`. Forms → `fetch()` with the form body + CSRF
  token, then the same extraction on the response.
- **Scripts and stylesheets** that the new page needs are deduped and hoisted
  to `<head>` (stylesheets) or cloned into the pane (scripts after
  `/static/app.js`). `NodexiaApp.rescan(pane)` re-runs the page's `init*`
  helpers on the swapped-in DOM.
- **Terminal** uses a `tab-activated` / `tab-deactivated` /
  `tab-closing` event surface (`terminal-tab-adapter.js` → `card.__nodexiaTerminal`)
  so the WebSocket and PTY keep running while the pane is backgrounded, and
  are disposed only when the tab itself is closed.

## Terminal WebSocket lifecycle

`internal/module/terminal/`:

1. `GET /servers/{id}/terminal` — renders the credential form (or the xterm
   card if a ticket is already present).
2. `POST /servers/{id}/terminal` — dials SSH with the submitted (or
   stored) credentials, mints a single-use 30-second ticket via
   `internal/terminalticket`, and re-renders the same page with the ticket
   embedded as `data-ticket` on `#terminal-card`.
3. `GET /servers/{id}/terminal/ws?ticket=…` — same-origin check, atomic
   ticket consumption, session limit (max 3/user), then WebSocket upgrade via
   `coder/websocket`. The server opens an SSH shell with `xterm-256color`,
   pipes `io.Pipe` for stdin and the WS writer for stdout, and runs a 30s
   server-side ping. The single-use ticket model means "reconnect" navigates
   back to the credential page rather than re-dialling the consumed socket.

Any `http.ResponseWriter` wrapper in the middleware chain must implement
`Hijacker` + `Flusher` + `Unwrap()` or the upgrade fails. The logging
middleware's `statusRecorder` does this explicitly.

## Auth, sessions, CSRF

- **Auth**: a single admin. Two HMAC cookies, both `HttpOnly` + `SameSite=Lax`:
  - `nodexia_session` — issued to every visitor, anchors the CSRF token.
  - `nodexia_auth` — set after login, carries the username and expiry.
- **CSRF**: synchronizer-token pattern. The CSRF middleware checks
  `Origin`/`Referer` (same-origin) AND the hidden `_csrf_token` form field
  against `DeriveCSRFToken(sessionID, secret)`. GET/HEAD/OPTIONS are exempt.
  Every form embeds `<input type="hidden" name="_csrf_token" value="{{ .CSRFToken }}">`
  via `middleware.GetCSRFToken(r.Context())`.
- **Login rate limiting**: `internal/ratelimit/`.
- **SSH credentials are runtime-only** — the DB stores only strategy and
  reference metadata, never the password/key. The shared
  `internal/sshclient.Service` owns host-key pinning (trust on first use).

## Bilingual UI (en / fa + RTL)

- Server-rendered strings live in `internal/i18n/locales/{en,fa}.json`. The
  parity test in `internal/i18n/parity_test.go` enforces key equality.
- Handlers resolve server-side strings via `page.T("key", args…)`.
- Client JS strings live in the `js.*` namespace and are exported to the
  browser via `internal/view/clienti18n.go`. Look them up with `I18N.t(...)`.
- Telegram alert and digest text is template English only — the notify layer
  has no request locale.

## Background work

`internal/scheduler/` runs tickers for:

- Monitoring sweeps (resource + traffic collection)
- Node discovery sweeps
- Analytics rollup and cleanup
- Alert evaluation
- GeoIP country resolution
- Status digest (opt-in periodic Telegram summary, `NODEXIA_DIGEST_*`)

The first digest send is one interval after startup, never immediately. Every
scheduler failure is logged and swallowed — it must never disrupt other jobs.

## Module invariants to preserve

These are the non-obvious behaviours future changes must not silently break.
(See `CHANGELOG.md` for the history.)

- **bulk**: POST never runs SSH inline — it creates an in-memory job and
  redirects to a 2 s auto-refresh page. 5-worker pool. Own timeouts (reboot
  2m, update 20m). Sudo preamble exit codes: 88 = needs password, 87 = no
  package manager. Servers without credentials are skipped, never dropped.
- **nodes**: provider/driver architecture (`DefaultProviders()`); never
  hardcode node names — they come from remote config and must pass
  `ValidateNodeName`. Actions and installs run as background
  `commandstream`/job sessions. The PasarGuard install drives the official
  script over positional stdin (fragile — re-count prompts if it breaks)
  then patches `/opt/<name>/.env`; generated shell must stay
  single-quote-free inside `sh -c '...'` (`TestGeneratedShellSyntax` guard).
  Discovery + Docker actions use `sudo -n`. One discovery sweep = one
  `created_at`. API key/cert are shown once, never persisted.
- **terminal**: see "Terminal WebSocket lifecycle" above. Vendored xterm.js
  v5.5.0 + the `@xterm/addon-*` suite (fit, unicode11, web-links, search,
  serialize, webgl, canvas) live in `web/static/`. The CSP is
  `script-src 'self'` — no CDN. Renderer is WebGL on desktop (canvas
  fallback), canvas on mobile.
- **commands**: `run` / `stream` start background sessions (never inline);
  `test` is a sync connection check; interactive TUIs (top, vim, mysql,
  `tail -f` …) are detected server-side and redirected to the terminal
  (`data-interactive-programs` is the shared source of truth).
- **monitoring**: each sweep stores ONE latest vnStat snapshot per server
  (JSON daily/monthly rows). Daily rows are tailed to 35
  (`retainedDailyRows`, 5 weeks) so the seasonal forecast has enough
  per-weekday samples AND a full anchored billing period can be summed from
  dailies; monthly rows to 6. `TotalBytes` always defaults to `RX+TX`.
  Forecast / analytics default to download (RX) but follow the limit's
  series kind (rx/tx/total).
- **analytics / forecast**: `ForecastService.ComputeWithConfig(days, months, cfg)`
  selects an algorithm by history length (trend < 7 ≤ MA < 14 ≤ WMA < 21 ≤
  seasonal); `Compute` is the calendar-month/RX wrapper. The accounting
  period follows `servers.traffic_reset_day` (1 = calendar month, 2–28 =
  billing anchor): calendar periods use the authoritative monthly row,
  anchored periods sum the daily series. `computeExhaustion` is bounded to
  the period end and reuses the SAME per-day predictor as the period
  projection so they can't disagree.
- **alerts**: the evaluator persists events, cooldowns, and consecutive-hit
  streaks. A metric that is unavailable this cycle is **skipped** (neither
  fired nor resolved) to keep streaks/cooldowns sane across gaps; recovery
  resolves an open event even while silenced. Predictive comparators /
  thresholds are normalised in validation — don't let the operator store an
  incoherent condition.

## Database conventions

- Portable SQL: explicit column lists, `?` placeholders, `LastInsertId()` not
  `RETURNING`. All shared paths go through `db.DBTX`.
- Schema lives in `schema.sql` and is append-only — never rewrite or delete
  statements. New migrations are added at the bottom with a version comment.
- `internal/db/` wraps both SQLite and (planned) MySQL behind the same
  interface so handlers stay driver-agnostic.

## Workflow

- Tests live beside code (`*_test.go`); helpers in `internal/testutil`.
- Run `go vet ./...` and `make test` after changes.
- Mirror an existing module's structure for a new feature instead of
  inventing one.
- Prefer small, surgical changes.
- Conventional Commits: `type(scope): subject`, lowercase, imperative,
  ≤ ~50 chars.

```bash
make build              # → bin/nodexia
make test               # go test ./...
go run ./cmd/nodexia/   # local dev (needs NODEXIA_AUTH_USERNAME/PASSWORD)
go vet ./...
```
