# Nodes Module

> Architecture, lifecycle, and operational guide for Nodexia's Rebecca and PasarGuard node discovery, monitoring, and deployment subsystem.
> For overall system conventions, see [`docs/architecture.md`](architecture.md); for UI rules, see [`FRONTEND.md`](../FRONTEND.md).

---

## 1. Overview

The `nodes` module (`internal/module/nodes/`) discovers, monitors, and manages **Rebecca** and **PasarGuard** VPN node installations across managed Linux servers, supporting both **Docker** and **systemd** runtimes.

Key capabilities:
- **Automated & On-Demand Discovery:** Detects engine variant, runtime mode, version, Xray listening ports, latency, and last-seen timestamp.
- **30-Day Uptime History:** Maintains UTC daily availability buckets for fleet health visibility.
- **Guided Deployment Wizard:** Multi-step interactive deployment with preflight validation, live progress stages, and cancellation.
- **Live Operations & Inspection:** Per-node lifecycle actions (`start`, `stop`, `restart`, `update`), read-only SSH config inspection, and a live streaming log drawer.

---

## 2. Discovery & Persistence Flow

```text
Scheduler (12h) or POST /servers/{id}/nodes (CSRF-protected)
  │
  ▼
AcquireServerSweep(ctx, serverID)        # Single-flight lock per server
  │
  ▼
sshclient.BatchSession                   # Single SSH connection across probes & port scans
  │
  ├──► Healthy / Clean Empty Sweep ──► ReplaceLatest(sweep_id) + upsert node_status_history (UTC day)
  └──► SSH / Provider Error        ──► Preserve cached snapshots & surface degraded/retry banner
```

### Key Guarantees
- **Read-Only GET:** `GET /servers/{id}/nodes` renders persisted snapshots and never triggers inline SSH discovery.
- **Atomic Snapshot Generation:** `SQLRepository` (`db.DBTX`) replaces `node_snapshots` atomically per `sweep_id` inside an explicit transaction.
- **Cache Preservation:** Failed or timed-out SSH sweeps never overwrite valid cached nodes with empty placeholders.
- **30-Day UTC Strip:** Daily uptime status is aggregated deterministically by UTC calendar day in `node_status_history`.

---

## 3. Deployment & Remote Actions

- **Rebecca Installs:** Allocates a non-echoing PTY (`umask 077`) and passes sensitive credentials/tokens over stdin rather than CLI arguments or shell history.
- **PasarGuard Installs:** Runs interactive prompt preflight checks prior to execution and requires explicit `confirm_override` consent before applying destructive `--override` flags.
- **Deploy Stage Streaming:** Internal `[NODEXIA:stage:...]` markers are parsed server-side and emitted cleanly as SSE `stage` events alongside `output`, `done`, and `error`.
- **Action Log Drawer:** Streams `commandstream` SSE events (`output`, `stderr`, `done`, `error`) with bounded line buffering, reconnect deduplication, pause/search/copy controls, and automatic teardown on tab close.

---

## 4. HTTP Routes

Registered in `internal/module/nodes/module.go`:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/servers/{id}/nodes` | Render Nodes workspace from cached snapshots (read-only) |
| `POST` | `/servers/{id}/nodes` | Trigger live SSH discovery refresh (CSRF-protected) |
| `POST` | `/servers/{id}/nodes/actions` | Execute node lifecycle action (`start`/`stop`/`restart`/`update`/logs) |
| `GET` | `/servers/{id}/nodes/stream/{stream}/events` | SSE stream for node action command output |
| `GET` | `/servers/{id}/nodes/credentials` | Fetch node panel credentials/metadata for deploy prefill |
| `GET` | `/servers/{id}/nodes/config` | Read-only inspection of node configuration/compose/env files |
| `POST` | `/servers/{id}/nodes/install` | Validate deploy form and launch background install job |
| `GET` | `/servers/{id}/nodes/install/{job}` | Standalone install status fallback page |
| `GET` | `/servers/{id}/nodes/install/{job}/events` | SSE stream for install stages and terminal logs |
| `POST` | `/servers/{id}/nodes/install/{job}/cancel` | Cancel an in-flight deploy job cleanly |

---

## 5. Frontend Architecture

- **Templates (`web/templates/`):** Composed of focused partials — `nodes.gohtml`, `nodes_header.gohtml`, `nodes_card.gohtml`, `nodes_deploy_modal.gohtml`, `nodes_deploy_review.gohtml`, `nodes_drawer.gohtml`, and `nodes_install.gohtml`.
- **Styles (`web/static/nodes.css`):** Layer 5 stylesheet using Dark Graphite tokens (`tokens.css`), strict CSS logical properties, responsive mobile bottom-sheet menus (`>= 44px` touch targets), and `prefers-reduced-motion` support.
- **JavaScript (`web/static/modules/nodes.js` & `shell.js`):** Zero-build, strict-CSP ES modules scoped per `.tab-pane` with cleanup registered via `registerPaneTeardown` (timers, SSE streams, observers).
- **Bidirectional & i18n:** Full English and Persian parity (`nodes.*` in `en.json` / `fa.json`), with `unicode-bidi: isolate` on IPs, ports, versions, and log output.
