# Nodexia

Nodexia is a lightweight, self-hosted control panel for monitoring and managing
Rebecca and Pasarguard panel nodes.

The project focuses on the work that matters most for node operators: traffic
usage, server resource health, node discovery, and a simple registry of managed
servers. SSH command execution and SFTP access are included as supporting tools,
but they are not the main direction of the product.

> Nodexia is still under active development and is not yet complete. It was
> built with AI assistance, so bugs, rough edges, and implementation mistakes may
> exist. Review, test, and harden it before using it in sensitive production
> environments.

## Core Focus

- **Traffic monitoring**: collect and review network usage snapshots for managed servers.
- **Resource monitoring**: track CPU, RAM, disk, load average, uptime, and network summaries.
- **Node management**: discover and inspect Rebecca and Pasarguard nodes with runtime evidence.
- **Server registry**: keep SSH targets organized with labels and credential strategies.
- **Background collection**: schedule recurring monitoring and node discovery jobs.
- **Operational visibility**: view recent jobs, collector output, diagnostics, and health checks.

## Supporting Tools

- **SSH command runner**: run maintenance or diagnostic commands when needed.
- **SFTP browser**: browse remote paths and download files for troubleshooting.
- **System facts**: collect OS, kernel, architecture, package update, and uptime details.
- **Admin login**: protect the panel with a single administrator account.
- **SSH host key pinning**: trust-on-first-use host key handling for managed servers.

## Quick Start

```bash
git clone https://github.com/Ho3einK84/Nodexia.git
cd Nodexia

cp .env.example .env
# Edit .env and set NODEXIA_AUTH_USERNAME and NODEXIA_AUTH_PASSWORD

go run ./cmd/nodexia/
```

Open `http://localhost:8080` and sign in with the configured admin account.

## Production Install

```bash
git clone https://github.com/Ho3einK84/Nodexia.git
cd Nodexia

sudo ./install.sh --domain panel.example.com --admin-user admin
```

The installer prepares the production environment and prompts for the admin
password.

## Docker

```bash
docker compose -f compose.production.yml up --build
```

## Important Routes

| Path | Purpose |
|------|---------|
| `/` | Dashboard overview |
| `/servers` | Managed server registry |
| `/servers/{id}/monitoring` | Resource and traffic monitoring |
| `/servers/{id}/nodes` | Rebecca/Pasarguard node discovery |
| `/servers/{id}/system` | System facts |
| `/servers/{id}/commands` | Supporting SSH command runner |
| `/servers/{id}/files` | Supporting SFTP browser |
| `/ops/diagnostics` | Operational diagnostics |
| `/healthz` | Health check |

## Configuration

Configuration is environment-based. See `.env.example` for the full list.

Key settings include admin credentials, session secret, database driver, HTTP
listen address, SSH timeouts, scheduler intervals, and host key policy.

## Build and Test

```bash
make build
make test
```

## License

MIT
