# 🛰️ Nodexia

> Lightweight, self-hosted control panel for monitoring and managing **Rebecca** and **Pasarguard** panel nodes.

![status](https://img.shields.io/badge/status-active%20development-orange) ![license](https://img.shields.io/badge/license-MIT-blue) ![go](https://img.shields.io/badge/go-1.25-00ADD8)

> ⚠️ **Under active development.** Built with AI assistance — expect bugs and rough edges. Review, test, and harden before using with sensitive production data.

---

## ✨ Features

- 📊 **Monitoring** — CPU, RAM, disk, load, uptime, and network/traffic snapshots per server.
- 📈 **Analytics & forecasting** — historical metrics, SVG charts, and bandwidth prediction with upload/download split.
- 🔍 **Node discovery** — detect and manage Rebecca / Pasarguard nodes with live evidence, plus a Pasarguard install wizard.
- 🔔 **Alerting** — threshold rules with Telegram delivery, silences, and history.
- ⏱️ **Scheduler** — recurring background monitoring and discovery jobs.
- 🧰 **Supporting tools** — bulk reboot/update/delete, in-browser SSH terminal (xterm.js over WebSocket), command runner, SFTP browser.
- 🔒 **Security** — single admin account, HMAC-signed cookie sessions, trust-on-first-use SSH host-key pinning, runtime-only credentials.

---

## 🚀 Production install

Nodexia runs as a Docker Compose stack behind [Caddy](https://caddyserver.com/) (automatic HTTPS). `install.sh` handles everything on a fresh Ubuntu host: installs Docker, deploys to `/opt/nodexia`, generates secrets, builds the containers, registers a systemd service, and waits for the app to report healthy.

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

The installer prompts for an admin username/password, then prints the panel URL and credentials once it's healthy. Add `--non-interactive` for an unattended run (random admin password printed once at the end).

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

**What it creates:** `/opt/nodexia` (source + Compose), `/opt/nodexia/.env.production` (secrets, `chmod 600`), the `nodexia.service` systemd unit, and the `nodexia_data` (SQLite + pinned host keys) and `caddy_data` (TLS certs) volumes — all persist across updates.

**Update**

```bash
cd Nodexia && git pull
sudo ./install.sh --domain panel.example.com   # rebuilds, preserves existing secrets
```

> 🛠️ **Manual / non-Ubuntu:** `cp .env.production.example .env.production`, edit it, then `docker compose -f compose.production.yml up -d --build`.

---

## ⚙️ Configuration

Configuration is environment-based — the full annotated list lives in [`.env.production.example`](.env.production.example). Edit `/opt/nodexia/.env.production` and run `sudo systemctl restart nodexia` to apply.

| Variable | Required | Description |
|----------|:--------:|-------------|
| `NODEXIA_AUTH_USERNAME` / `NODEXIA_AUTH_PASSWORD` | ✅ | Admin login. Empty or known-weak passwords are refused at production startup. |
| `NODEXIA_SESSION_SECRET` | ✅ | HMAC key for signed cookies; unique, ≥ 16 chars (`openssl rand -base64 48`). |
| `NODEXIA_DOMAIN` | ✅ | Public hostname; changing it re-issues certificates on restart. |
| `NODEXIA_TELEGRAM_BOT_TOKEN` | — | Telegram bot token for alerts; blank disables sending. |
| `NODEXIA_DB_DRIVER` / `NODEXIA_DB_DSN` | — | `sqlite` (default) or `mysql` + DSN. |
| `NODEXIA_SCHEDULER_MONITORING_INTERVAL` | — | Monitoring interval (default `15m`). |
| `NODEXIA_SSH_HOST_KEY_POLICY` | — | `tofu` (default) or `insecure`. |

> 🔐 **Never commit or share `.env.production`** — it holds your admin password, session secret, and bot token. It's gitignored.

---

## 🔔 Alerting

1. Create a bot with [@BotFather](https://t.me/BotFather) and copy its token.
2. Set `NODEXIA_TELEGRAM_BOT_TOKEN` and restart the stack.
3. In **`/alerts`**, add a channel (your chat ID), define rules (metric, threshold, severity, cooldown), and send a test message.

Without a token the alerts UI still records events — it just shows a "not configured" notice instead of sending.

---

## 💻 Local development

No Docker required:

```bash
git clone https://github.com/Ho3einK84/Nodexia.git
cd Nodexia
cp .env.example .env          # set NODEXIA_AUTH_USERNAME / NODEXIA_AUTH_PASSWORD
go run ./cmd/nodexia/
```

Open <http://localhost:8080> and sign in. Dev cookies aren't marked `Secure`, so plain HTTP works.

---

## 🗺️ Routes

| Path | Purpose |
|------|---------|
| `/` | Dashboard |
| `/servers` | Server registry |
| `/servers/{id}/monitoring` | Resource + traffic monitoring |
| `/servers/{id}/analytics` | Historical metrics + forecasting |
| `/servers/{id}/nodes` | Rebecca / Pasarguard discovery |
| `/servers/{id}/terminal` | In-browser SSH terminal |
| `/alerts` | Alert rules, channels, silences, history |
| `/servers/bulk` | Bulk reboot / update / delete |
| `/healthz` | Health check |

Also available: `/servers/{id}/system`, `/servers/{id}/commands`, `/servers/{id}/files`, `/ops/diagnostics`.

---

## 🧪 Build & test

```bash
make build    # → bin/nodexia
make test     # full test suite
go vet ./...  # static analysis
```

---

## 📄 License

MIT
