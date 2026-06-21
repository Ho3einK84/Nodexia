# 🛰️ Nodexia

> Lightweight, self-hosted control panel for monitoring and managing **Rebecca** and **Pasarguard** panel nodes.

![status](https://img.shields.io/badge/status-active%20development-orange) ![license](https://img.shields.io/badge/license-MIT-blue) ![go](https://img.shields.io/badge/go-1.25-00ADD8)

> ⚠️ **Under active development.** Built with AI assistance — expect bugs and rough edges. Review, test, and harden before using with sensitive production data.

Nodexia is a single Go binary with no client-side framework: server-rendered
HTML (`html/template`), the Go standard-library HTTP server, and a tiny pinned
set of dependencies. It connects to your nodes over SSH, collects resource and
traffic metrics on a schedule, and surfaces them through monitoring, analytics,
forecasting, and alerting.

---

## ✨ Features

- 📊 **Monitoring & analytics** — CPU, RAM, swap, disk, load, and uptime per
  server, plus vnStat traffic (daily/monthly download & upload), charted from
  hourly/daily rollups.
- 🔮 **Bandwidth forecasting** — projects today / this-week / this-month download
  from history (the model adapts as more data arrives) with a confidence level.
- 🚦 **Monthly limits & days-to-exhaustion** — set an optional per-server download
  cap; the forecast flags if you'll exceed it and how many days are left.
- 🔔 **Alerting** — threshold and **predictive** rules (warn *before* a limit is
  hit) with cooldowns, silences, severity, history, and Telegram delivery — plus
  an optional periodic status digest.
- 🔍 **Node discovery** — detect and manage Rebecca / Pasarguard nodes, with a
  Pasarguard install wizard and a background scheduler for monitoring/discovery.
- 🌐 **Bilingual UI** — full English and Persian (فارسی) with RTL support;
  installable as a PWA.
- 🧰 **Supporting tools** — bulk reboot/update/delete, in-browser SSH terminal,
  command runner, SFTP browser, and encrypted backup/restore.
- 🔒 **Security** — single admin, HMAC-signed CSRF-protected sessions, login rate
  limiting, SSH host-key pinning, and runtime-only SSH credentials.

---

## 🚀 Production install

Nodexia runs as a Docker Compose stack behind [Caddy](https://caddyserver.com/)
(automatic HTTPS). `install.sh` handles everything on a fresh Ubuntu host:
installs Docker, deploys to `/opt/nodexia`, generates secrets, builds the
containers, registers a systemd service, and waits for the app to report healthy.

**Prerequisites**

- Ubuntu 24.04 with root/sudo (only 24.04 is tested)
- A domain with a DNS `A` record pointing at the server's public IP
- Inbound TCP **80** + **443** open
- ≥ 1 vCPU / 1 GB RAM

**Install**

```bash
git clone https://github.com/Ho3einK84/Nodexia.git
cd Nodexia
sudo ./install.sh --domain panel.example.com --email you@example.com
```

The installer prompts for an admin username/password, then prints the panel URL
and credentials once it's healthy. Add `--non-interactive` for an unattended run
(random admin password printed once at the end).

**Common flags**

| Flag | Purpose |
|------|---------|
| `--domain <host>` | Public hostname (required for a new install) |
| `--email <addr>` | ACME / Let's Encrypt contact |
| `--admin-user` / `--admin-password` | Admin login (preserved on rerun unless set) |
| `--telegram-bot-token <token>` | Enable Telegram alert delivery |
| `--non-interactive` | Never prompt; auto-generate missing values |
| `--skip-dns-check` / `--skip-port-check` | Skip preflight checks |
| `-h`, `--help` | Show all options |

**What it creates:** `/opt/nodexia` (source + Compose),
`/opt/nodexia/.env.production` (secrets, `chmod 600`), the `nodexia.service`
systemd unit, and the `nodexia_data` (SQLite + pinned host keys) and `caddy_data`
(TLS certs) volumes — all persist across updates.

**Update**

```bash
cd Nodexia && git pull
sudo ./install.sh --domain panel.example.com   # rebuilds, preserves existing secrets
```

> 🛠️ **Manual / non-Ubuntu:** `cp .env.production.example .env.production`, edit
> it, then `docker compose -f compose.production.yml up -d --build`.

---

## 📸 Screenshots

> Demo data, shown purely to illustrate the interface.

**Dashboard** — health, traffic, and collection status across every server.

![Dashboard](docs/screenshots/01-dashboard.png)

**Server registry** — your shared Rebecca / Pasarguard hosts, with country,
tags, and quick actions.

![Server registry](docs/screenshots/02-servers.png)

**Resource monitoring** — live CPU / RAM / disk gauges, load average, uptime.

![Monitoring](docs/screenshots/03-monitoring.png)

**Bandwidth forecasting & monthly limits** — projects this-month download and
flags whether you'll exceed the cap (and how many days are left).

Projected to exceed the limit:

![Forecast — exceeding limit](docs/screenshots/04-analytics-forecast.png)

On track to stay under the limit:

![Forecast — within limit](docs/screenshots/05-analytics-forecast-safe.png)

**Node discovery** — detected nodes per host (handy when one server is shared
across several panels).

![Nodes](docs/screenshots/06-nodes.png)

**Alerting** — threshold and predictive rules with Telegram delivery.

![Alerts](docs/screenshots/07-alerts.png)

**Bilingual UI** — full Persian (فارسی) interface with RTL layout.

![Persian dashboard](docs/screenshots/08-dashboard-fa.png)

---

## ⚙️ Configuration

Configuration is environment-based — the full annotated list lives in
[`.env.production.example`](.env.production.example). Edit
`/opt/nodexia/.env.production` and run `sudo systemctl restart nodexia` to apply.

| Variable | Required | Description |
|----------|:--------:|-------------|
| `NODEXIA_AUTH_USERNAME` / `NODEXIA_AUTH_PASSWORD` | ✅ | Admin login. Empty or known-weak passwords are refused at production startup. |
| `NODEXIA_SESSION_SECRET` | ✅ | HMAC key for signed cookies; unique, ≥ 16 chars (`openssl rand -base64 48`). |
| `NODEXIA_DOMAIN` | ✅ | Public hostname; changing it re-issues certificates on restart. |
| `NODEXIA_TELEGRAM_BOT_TOKEN` | — | Telegram bot token for alerts/digest; blank disables sending. |
| `NODEXIA_DB_DRIVER` / `NODEXIA_DB_DSN` | — | `sqlite` (default) or `mysql` + DSN. |
| `NODEXIA_SCHEDULER_MONITORING_INTERVAL` | — | Monitoring interval (default `15m`). |
| `NODEXIA_SCHEDULER_NODES_INTERVAL` | — | Node discovery interval (default `12h`). |
| `NODEXIA_DIGEST_ENABLED` | — | Enable the periodic Telegram status digest (default `false`). |
| `NODEXIA_DIGEST_INTERVAL` | — | Digest cadence when enabled (default `24h`). |
| `NODEXIA_DIGEST_CHANNEL` | — | Channel name to send the digest to; empty = every enabled channel. |
| `NODEXIA_SSH_HOST_KEY_POLICY` | — | `tofu` (default) or `insecure`. |

> 🔐 **Never commit or share `.env.production`** — it holds your admin password,
> session secret, and bot token. It's gitignored.

---

## 🔔 Alerting & digest

1. Create a bot with [@BotFather](https://t.me/BotFather) and copy its token.
2. Set `NODEXIA_TELEGRAM_BOT_TOKEN` and restart the stack.
3. In **`/alerts`**, add a channel (your chat ID), define rules (metric,
   threshold, severity, cooldown, consecutive hits), add silences as needed, and
   send a test message.

Without a token the alerts UI still records events — it just shows a "not
configured" notice instead of sending.

**Predictive metrics.** To use the forecast-derived alert metrics
(`projected_exceed_limit`, `days_to_exhaustion`), first set a **monthly download
limit** for the server on its analytics page (`/servers/{id}/analytics`). These
rules only apply once a limit is configured and enough history exists to project;
otherwise they are skipped that cycle (never falsely firing or resolving).

**Status digest.** Set `NODEXIA_DIGEST_ENABLED=true` (a bot token must be
configured) to receive a recurring summary every `NODEXIA_DIGEST_INTERVAL`. The
first digest is sent one interval after startup, never immediately.

---

## 💻 Local development

No Docker required:

```bash
git clone https://github.com/Ho3einK84/Nodexia.git
cd Nodexia
cp .env.example .env          # set NODEXIA_AUTH_USERNAME / NODEXIA_AUTH_PASSWORD
go run ./cmd/nodexia/
```

Open <http://localhost:8080> and sign in. Dev cookies aren't marked `Secure`, so
plain HTTP works.

---

## 🗺️ Routes

| Path | Purpose |
|------|---------|
| `/` | Dashboard |
| `/servers` | Server registry |
| `/servers/{id}/monitoring` | Resource + traffic monitoring |
| `/servers/{id}/analytics` | Historical metrics, forecasting, monthly limit |
| `/servers/{id}/nodes` | Rebecca / Pasarguard discovery + install |
| `/servers/{id}/terminal` | In-browser SSH terminal |
| `/alerts` | Alert rules, channels, silences, history |
| `/servers/bulk` | Bulk reboot / update / delete |
| `/ops/diagnostics` | Scheduler overview, backup / restore |
| `/healthz` | Health check (`/healthz/live`, `/healthz/ready`) |
| `/lang/{code}` | Switch UI language (en / fa) |
| `/manifest.webmanifest`, `/sw.js` | PWA manifest + service worker |

Also available: `/servers/{id}/system`, `/servers/{id}/commands`,
`/servers/{id}/files`.

---

## 🗄️ Data & retention

- **Database:** SQLite by default (MySQL supported via `NODEXIA_DB_DRIVER=mysql`).
  Schema and migrations live in [`schema.sql`](schema.sql) and are applied
  automatically on startup.
- **Raw system snapshots** are kept ~30 days, then **hourly rollups** (~6 months)
  and **daily rollups** (~2 years) become the authoritative time series.
- **Traffic snapshots** retain the latest ~35 days of daily vnStat rows (5 weeks)
  so the day-of-week seasonal forecast has enough samples per weekday, plus ~6
  months of monthly totals.

---

## 🧪 Build & test

```bash
make build    # → bin/nodexia
make test     # full test suite
go vet ./...  # static analysis
```

> **Go version:** the project targets the **latest Go 1.25.x** patch, not a
> frozen one. The Docker base image (`golang:1.25`) and CI (`go-version: 1.25.x`)
> both float to the newest 1.25 patch automatically. The `go 1.25.0` line in
> `go.mod` is the **minimum** language version required — Go has no directive
> that auto-selects the latest patch, so building with any 1.25.x toolchain
> (1.25.0 or newer) is supported and no `toolchain` pin is added.

---

## 📄 License

MIT
