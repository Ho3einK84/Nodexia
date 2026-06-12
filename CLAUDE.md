# CLAUDE.md

Guidance for Claude Code when working in the Nodexia repository.

## Product

Nodexia is a lightweight, self-hosted control panel focused on **monitoring and
managing Rebecca and Pasarguard panel nodes**. The product priorities, in order:

1. Traffic monitoring (network usage snapshots per server)
2. Resource monitoring (CPU, RAM, disk, load average, uptime, network)
3. Node management (discover/inspect Rebecca & Pasarguard nodes)
4. Server registry (SSH targets with labels + credential strategies)
5. Background collection (scheduled monitoring + node discovery jobs)

SSH command execution, SFTP browsing, bulk server actions, and in-browser SSH
terminal are **supporting tools only**, not the main direction. They have been
explicitly added and must not be expanded further without being asked.

## Stack & hard constraints

- Go 1.25, **standard library only** for HTTP: `net/http` + `ServeMux`.
  Do NOT add a web framework (gin/echo/fiber/chi).
- Rendering is **SSR with `html/template`**. No SPA, no JS framework, no client
  rendering. Keep `web/static` minimal.
- Dependencies are deliberately few (sftp, mysql driver, modernc sqlite,
  x/crypto, coder/websocket). Justify any new module before adding it.
  `github.com/coder/websocket v1.8.14` is pinned for the WebSocket terminal.
- DB: SQLite now (`modernc.org/sqlite`, pure-Go), MySQL migration planned.
  ALL SQL must stay portable across both:
  - explicit column lists
  - `?` placeholders
  - use `LastInsertId()`, NOT SQLite-only `RETURNING`
  - go through the `db.DBTX` interface in shared paths

## Layout

- `cmd/nodexia/main.go` — entrypoint
- `internal/app/`        — application core, bootstrap, module wiring
- `internal/config/`     — typed env-based config (`NODEXIA_*`, `.env`)
- `internal/db/`         — driver resolution, migrations, `DBTX` contract
- `internal/http/`       — router, handlers, middleware, httperrors
- `internal/module/`     — feature modules + `registry`
- `internal/scheduler/`  — background job runtime (monitoring, nodes)
- `internal/sshclient/`  — shared SSH runtime
- `internal/commandstream/` — in-memory live command output store
- `internal/terminalticket/` — single-use ticket store for terminal sessions
- `internal/ratelimit/`  — login throttle
- `internal/view/`       — `Renderer`, `PageData`, nav
- `web/templates/`, `web/static/` — SSR templates and assets
- `schema.sql` — schema + migration bookkeeping

## Module pattern (follow `servers` and `nodes` as references)

Every feature is a `module.Module`:
- `Name() string`, `RouteGroup() string`, `RegisterRoutes(mux, deps)`
- Register it in `internal/module/registry/registry.go` → `DefaultModules()`
- `RegisterRoutes` MUST check its dependencies (DB / SSH) and fall back to
  `module.NewPlaceholderHandler(...)` when they are unavailable — never panic on
  a missing runtime.
- Routes are server-scoped: `/servers/{id}/<group>` (e.g. `/servers/{id}/nodes`).
  Read `{id}` via `r.PathValue("id")`.

## Repository pattern (follow `internal/module/servers`)

- Define the domain type + `Repository` interface in `repository.go`.
- Implement in `sql_repository.go` as a `SQLRepository` over `*sql.DB`.
- Wrap multi-statement writes in `BeginTx` / `Rollback` / `Commit`.
- Wrap errors with package context: `fmt.Errorf("servers: create: %w", err)`.
- Export a package-local `ErrNotFound` and map `sql.ErrNoRows` to it.

## HTTP, security, rendering

- Auth: single admin account; two HMAC-signed cookies (`nodexia_session` for
  CSRF anchor, `nodexia_auth` for identity). Both HttpOnly, SameSite=Lax.
- Every HTML form must embed the CSRF token: hidden field `_csrf_token`,
  populated from `middleware.GetCSRFToken(r.Context())`.
- Render through `deps.Renderer.Render(...)` with a `view.PageData`; set
  `ContentTemplate`, `Title`, `ActiveNav`, etc. — match existing handlers.
- Reuse existing middleware (request ID, logging, panic recovery, security,
  auth, login throttle). Don't reinvent them.

## Bulk actions (`internal/module/bulk`)

- Routes: `POST /servers/bulk` (starts a job, 303-redirects) and
  `GET /servers/bulk/jobs/{job}` (live result page).
- Supported actions: `reboot`, `update` (packages), `delete`.
- **Background jobs**: the POST never executes SSH work inline — it resolves
  targets, creates an in-memory job (`jobs.go`, 30 min TTL after finish), and
  redirects to the job page, which auto-refreshes every 2 s while rows are
  `pending`/`running`. Synchronous execution previously outlived the HTTP
  write timeout and surfaced as 502s — do not reintroduce it.
- **Timeouts**: bulk SSH commands use their own generous limits
  (`bulkRebootTimeout = 2m`, `bulkUpdateTimeout = 20m`), NOT the global SSH
  command timeout (20 s default — far too short for package upgrades).
- **Worker pool**: exactly `bulkWorkers = 5` goroutines; bounded by a closed
  buffered channel consumed via `range`.
- **Sudo preamble** (non-interactive): checks `id -u` first; if not root,
  tries `sudo -n true`. Exit code **88** = sudo requires a password; exit
  code **87** = no supported package manager found.
- Servers without stored credentials are **skipped** (never silently dropped).
- UI: form always visible (progressive enhancement); `bulk.js` only adds the
  live count label and select-all indeterminate state.

## Nodes (`internal/module/nodes`)

- **Provider/driver architecture**: each node family implements
  `nodes.Provider` (discovery probe + parser, official-CLI action commands,
  install support). New node types are added by implementing the interface
  and registering it in `DefaultProviders()` — never assume a single node or
  hardcode node names; instance names come from remote configuration.
- **PasarGuard** (`pasarguard.go`): Docker based, multi-instance. Each
  instance `<name>` owns `/opt/<name>` (compose + `.env`) and
  `/var/lib/<name>` (certs, xray core). Discovery scans
  `/opt/*/docker-compose.yml` + `docker ps -a`. Actions run
  `pg-node --name <name> <op>` with non-interactive flags (`-n`/`--no-follow`
  to avoid log tailing, `--yes` for confirmations); if the `pg-node` command
  is missing, the per-instance CLI `/usr/local/bin/<name>` is used (the
  official script installs it under the custom name).
- **PasarGuard install wizard**: `POST /servers/{id}/nodes/install` runs the
  official script (`install --name <name> --yes`) as a background job
  (`install.go`, in-memory store, 30 min TTL). The script tails container
  logs forever after installing, so the remote command is bounded with
  `timeout` and exit 124 is NOT a failure — success is decided by the
  post-install verification probe that reads `/opt/<name>/.env` (API key)
  and `/var/lib/<name>/certs/ssl_cert.pem`. The API key and certificate are
  shown once on the job page for panel registration and are never persisted.
- **Rebecca** (`rebecca.go`): detect and manage only — no install option.
  Config from `/opt/rebecca-node/.env`, version from `.binary-release.json`
  (`tag` field), health from systemd/docker. Actions run the `rebecca-node`
  CLI; confirm prompts are answered with `yes |`.
- **Node actions** (`POST /servers/{id}/nodes/actions`) run as background
  `commandstream` sessions (303 → `?stream=` polling page). They require
  stored credentials, and the submitted (type, name) pair must exist in the
  latest stored discovery sweep. Any node name interpolated into a shell
  command MUST pass `ValidateNodeName`.
- Discovery snapshots persist in `node_snapshots` (one batch per sweep via
  `ReplaceLatest`); the scheduler's nodes job uses the same providers.

## Commands (`internal/module/commands`)

- Intents on `POST /servers/{id}/commands`: `run`/`stream` (both start a
  background stream session and 303-redirect to the polling live page —
  never run a command inside the request), `test` (synchronous SSH
  connection test, bounded by the connect timeout), and `terminal`
  (303-redirect to `/servers/{id}/terminal?init=<cmd>`).
- **Interactive detection** (`interactive.go`): TUI/REPL commands (top, vim,
  ssh, mysql, `tail -f`, …) are detected server-side and redirected to the
  terminal even when submitted as `run`/`stream`. The program list is also
  exposed to the page via `data-interactive-programs` for the client-side
  hint — keep that single source of truth.

## Terminal (`internal/module/terminal`)

- Route group: `/servers/{id}/terminal` (GET form / POST issue-ticket /
  GET `/terminal/ws` WebSocket upgrade).
- **Credential flow**: STORED strategy skips the form and creates a ticket on
  GET. RUNTIME strategy shows a CSRF-protected POST form; credentials are
  **never persisted, logged, or placed in any URL**.
- **Ticket store** (`internal/terminalticket`): 30 s TTL, atomic single-use
  consume, per-user concurrent session cap (max `maxTerminalSessionsPerUser =
  3`). Ticket ID flows only via the `data-ticket` HTML attribute → JS.
- **WebSocket protocol** (JSON text frames):
  - Client → server: `{"type":"input","data":"..."}` or
    `{"type":"resize","cols":N,"rows":N}`
  - Server → client: `{"type":"output","data":"..."}` or
    `{"type":"error","message":"..."}`
- **Backpressure**: 5 s per-frame write deadline; slow clients cause the SSH
  session to terminate rather than accumulating output in memory.
- **Same-origin check**: `middleware.ValidateSameOriginRequest(r)` is called
  before `cwebsocket.Accept`. The accept options set `InsecureSkipVerify:
  true` only because we already validated origin manually.
- **Hijack passthrough**: any middleware that wraps `http.ResponseWriter`
  (currently the logging `statusRecorder`) MUST implement `http.Hijacker`
  (clearing connection deadlines after hijack), `http.Flusher`, and
  `Unwrap()` — otherwise the WebSocket upgrade fails (client sees close
  code 1006) or the connection dies when the server write timeout elapses.
- **CSP**: `connect-src 'self'` was added to the security middleware to allow
  the WebSocket upgrade from the same origin.
- **Vendored assets** (served via `GET /static/`, `script-src 'self'`):
  - `xterm v5.5.0` — `web/static/xterm.min.js`
    sha256: `4196e242ef1cf4c2adead8d97f4a772a69576076f70b095e004b4abbb049e7bf`
  - `xterm.css` — `web/static/xterm.min.css`
    sha256: `f7f724aea2bb620a6482bfb8e4bdecfae1152b0c7facef55fbda61f3b6cfedb2`
  - `@xterm/addon-fit v0.10.0` — `web/static/xterm-addon-fit.min.js`
    sha256: `a6a7bbb33569f16aa3e18d71425e34d035fc89a0b7e8cba084f8855f91aa38f1`

## Secrets & SSH

- SSH credentials are entered at runtime and are NOT persisted as secrets.
  The DB stores only credential *strategy* + *reference* metadata. Keep it that way.
- Use the shared `sshclient.Service` and respect configured connect/command
  timeouts. Host keys use trust-on-first-use pinning — preserve that flow.

## Commands

```bash
make build        # go build -trimpath -ldflags=... -o bin/nodexia ./cmd/nodexia
make test         # go test ./...
go run ./cmd/nodexia/   # local dev; needs NODEXIA_AUTH_USERNAME/PASSWORD in .env
go vet ./...
```

## Git workflow

- Work on the `main` branch only. Do not create, switch to, or push other
  branches unless explicitly asked.
- Commits MUST follow Conventional Commits
  (`type(scope): subject`, e.g. `feat(nodes): add pasarguard detector`).
  Common types: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`.
  Subject in lowercase, imperative mood, no trailing period, ≤ ~50 chars.
  Add a body explaining *why* when the change is non-trivial.
- All commits and pushes must be authored by the GitHub account
  **Ho3einK84** (`ho3ein.cyber@yahoo.com`). Use:

  ```bash
  git config user.name  "Ho3einK84"
  git config user.email "ho3ein.cyber@yahoo.com"
  ```

- Push to `origin main` only.

## Workflow expectations

- There are real tests next to the code (`*_test.go`) and helpers in
  `internal/testutil`. Add/extend tests for new logic, especially pure parsing
  (collectors, traffic, validation) and SQL repositories.
- Run `go vet ./...` and `make test` after changes.
- Before adding a feature, decide which module/route group it belongs to and
  mirror that module's existing files rather than introducing new structure.
- Prefer small, surgical changes consistent with surrounding code.
