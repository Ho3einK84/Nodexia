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

SSH command execution and SFTP browsing are **supporting tools only**, not the
main direction. Do not expand them into general-purpose server management without
being asked.

## Stack & hard constraints

- Go 1.25, **standard library only** for HTTP: `net/http` + `ServeMux`.
  Do NOT add a web framework (gin/echo/fiber/chi).
- Rendering is **SSR with `html/template`**. No SPA, no JS framework, no client
  rendering. Keep `web/static` minimal.
- Dependencies are deliberately few (sftp, mysql driver, modernc sqlite,
  x/crypto). Justify any new module before adding it.
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
