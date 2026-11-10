# 🛰️ Nodexia

> **Lightweight, single-binary control plane for managing and monitoring server fleets, Rebecca, and PasarGuard VPN nodes.**

[![status](https://img.shields.io/badge/status-active%20development-orange)](https://github.com/Ho3einK84/Nodexia)
[![license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![go](https://img.shields.io/badge/go-1.26-00ADD8)](https://golang.org)
[![zero--build](https://img.shields.io/badge/frontend-zero--build-success)](FRONTEND.md)

Nodexia connects to your Linux servers over SSH, gathers resource and vnStat traffic metrics on a schedule, projects usage against billing cycles, and delivers instant alerts. It delivers all of this through a blazing-fast, bilingual (English & Persian RTL) web UI embedded into a single, self-contained Go binary.

---

## ✨ Highlights

- 📊 **Monitoring & Analytics:** Real-time CPU, RAM, disk, load, and vnStat traffic rollups.
- 🔮 **Predictive Forecasting:** Adapting usage projections anchored to your provider's monthly billing reset day.
- 🚦 **Traffic Quotas & Fleet Warnings:** Hard and soft limits with days-to-exhaustion indicators and dismissible dashboard warning cards.
- 🔍 **Node Discovery & Deployment:** Automatically detect and manage Rebecca and PasarGuard nodes, complete with an interactive deployment wizard and 30-day uptime indicators.
- 🔔 **Multi-Engine Alerting:** Threshold, predictive, anomaly, and host-down alert rules delivered straight to Telegram.
- 🗂️ **Multi-Tab Workspace:** Open multiple servers, terminals, and metrics tabs concurrently without full page reloads.
- 🌐 **Bilingual & Responsive:** Full English and Persian (فارسی) support with bidirectional layout (LTR/RTL) and installable PWA.
- 🧰 **Built-In Ops Suite:** In-browser SSH terminal (xterm.js + WebSockets), remote command runner, SFTP file browser, and batch reboot/update tasks.
- ⚡ **Zero-Build Architecture:** No `npm`, `vite`, or Webpack. Pure Go SSR templates, browser-native ES Modules, and CSS design tokens.

---

## 🏗️ Architecture at a Glance

| Layer | Technology | Details |
|---|---|---|
| **Backend** | Go 1.26+ | Standard library HTTP (`net/http`), no heavy web frameworks |
| **Database** | SQLite / MySQL | Pure Go SQLite (`modernc.org/sqlite`) default; MySQL 8+ supported |
| **Frontend** | Zero-Build | Native ES Modules (`app.js` + 5 modules) and pure CSS tokens |
| **Packaging** | Single Binary | HTML templates and static assets embedded via Go `embed.FS` |
| **Security** | Production-Hardened | Single admin, HMAC CSRF protection, rate limiting, pinned host keys |

---

## 🚀 Quick Install

Nodexia installs on **Ubuntu 24.04** in seconds via automated Docker Compose with [Caddy](https://caddyserver.com/) providing automatic HTTPS:

```bash
git clone https://github.com/Ho3einK84/Nodexia.git
cd Nodexia
sudo ./install.sh --domain panel.example.com --email you@example.com
```

> **Unattended install:** add `--non-interactive` to automatically generate random secure credentials.

### 🧭 CLI Management

The installer sets up a global `nodexia` command:

```bash
nodexia status                 # Check container health and status
nodexia logs -f                # Follow live application logs
nodexia restart                # Restart application stack
nodexia update                 # Upgrade to latest release keeping configuration
nodexia uninstall --purge      # Remove stack and wipe data
```

---

## ⚙️ Essential Configuration

Configuration is managed through `/opt/nodexia/.env.production` (see [`.env.production.example`](.env.production.example)):

| Variable | Description | Default |
|---|---|---|
| `NODEXIA_AUTH_USERNAME` | Admin login username | Required |
| `NODEXIA_AUTH_PASSWORD` | Admin login password | Required |
| `NODEXIA_SESSION_SECRET` | 48-char random string for HMAC cookies | Required |
| `NODEXIA_DOMAIN` | Public domain for Caddy HTTPS | Required |
| `NODEXIA_TELEGRAM_BOT_TOKEN` | Bot token for alerts and daily digests | Optional |
| `NODEXIA_DB_DRIVER` | Database engine (`sqlite` or `mysql`) | `sqlite` |

---

## 📚 Documentation

- [**Frontend Architecture (`FRONTEND.md`)**](FRONTEND.md) — Zero-Build philosophy, template partials, ES modules, and CSS design tokens.
- [**System Architecture (`docs/architecture.md`)**](docs/architecture.md) — Request lifecycle, database model, module invariants, and security patterns.
- [**Multi-Tab Workspace (`docs/tab-system.md`)**](docs/tab-system.md) — In-page multi-tab lifecycle, state isolation, and terminal adapters.
- [**Contributing Guide (`CONTRIBUTING.md`)**](CONTRIBUTING.md) — Local development, code standards, and PR guidelines.

---

## 💻 Local Development

```bash
# 1. Setup local environment
cp .env.example .env

# 2. Run application
go run ./cmd/nodexia/

# 3. Test & static analysis
make test              # Run unit and integration tests
go vet ./...           # Static code inspection
make build             # Compile static binary to bin/nodexia
```

Access the local instance at `http://localhost:8080`.

---

## 📄 License

[MIT License](LICENSE) © 2026 Ho3einK84
