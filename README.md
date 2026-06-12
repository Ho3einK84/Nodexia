# Nodexia

Nodexia is a lightweight, self-hosted control panel for monitoring and managing
Rebecca and Pasarguard panel nodes.

> Nodexia is still under active development and is not yet complete. It was
> built with AI assistance, so bugs, rough edges, and implementation mistakes may
> exist. Review, test, and harden it before using it in sensitive production
> environments.

It focuses on the work that matters most for node operators — traffic usage,
server resource health, node discovery, alerting, and a simple registry of
managed servers. SSH command execution and SFTP access are included as
supporting tools, but they are not the main direction of the product.

**Highlights**

- Traffic and resource monitoring (CPU, RAM, disk, load, uptime, network).
- Rebecca / Pasarguard node discovery with runtime evidence.
- Threshold-based alerting with Telegram delivery, silences, and history.
- Background scheduler for recurring monitoring and discovery jobs.
- **Bulk actions**: reboot, update packages, or delete multiple servers at once
  with a bounded worker pool and per-server result summary.
- **In-browser SSH terminal**: xterm.js PTY over WebSocket with one-time
  tickets, a per-user session cap, and credentials that never touch disk or URLs.
- Single admin account, signed-cookie sessions, and trust-on-first-use SSH host
  key pinning.

---

## Production Installation

The supported production deployment runs Nodexia as a Docker Compose stack behind
a [Caddy](https://caddyserver.com/) reverse proxy that automatically provisions
and renews HTTPS certificates. The `install.sh` script automates the entire
process on a fresh Ubuntu server: it installs Docker, lays down the application
under `/opt/nodexia`, generates secrets, builds the containers, registers a
systemd service, and waits for the app to report healthy.

### Prerequisites

- **Ubuntu 24.04** server with `root` (or `sudo`) access. Other versions may
  work but only 24.04 is tested.
- A **domain name** whose DNS `A` record already points to the server's public
  IPv4 address. The installer verifies this before requesting certificates.
- Inbound **TCP ports 80 and 443** open to the internet (used by Caddy for ACME
  and HTTPS traffic).
- At least **1 vCPU and 1 GB RAM** recommended.

### Step 1 — Get the source

```bash
git clone https://github.com/Ho3einK84/Nodexia.git
cd Nodexia
```

Clone the repository and change into it. The installer runs from this directory
and copies the source into the install directory for you.

### Step 2 — Run the installer

```bash
sudo ./install.sh --domain panel.example.com --email you@example.com
```

This single command provisions everything:

- Installs `git`, `curl`, Docker Engine, and the Docker Compose plugin if missing.
- Verifies the domain's DNS record resolves to this server and that ports 80/443
  are free.
- Copies the source to `/opt/nodexia` and writes `/opt/nodexia/.env.production`
  (file mode `600`), generating a strong random `NODEXIA_SESSION_SECRET`.
- Prompts for an **admin username and password** (interactive mode) and stores
  them in the configuration.
- Generates `deploy/Caddyfile` for your domain, builds the images, and starts the
  `app` + `caddy` containers.
- Installs and enables the `nodexia.service` systemd unit so the stack restarts
  on boot.
- Waits for the application health check and confirms HTTPS is reachable.

**Common flags**

| Flag | Description |
|------|-------------|
| `--domain <host>` | Public hostname to serve and request certificates for. Required for a new install. |
| `--email <address>` | Contact email used for Let's Encrypt / ACME (recommended). |
| `--admin-user <name>` | Admin username. Defaults to `admin`; preserved on rerun unless provided. |
| `--admin-password <pass>` | Admin password. Preserved on rerun unless provided; auto-generated in non-interactive mode. |
| `--telegram-bot-token <token>` | Telegram bot token for alert delivery (optional; can also be set later by editing the config). |
| `--install-dir <path>` | Install directory. Default `/opt/nodexia`. |
| `--non-interactive` | Never prompt; fail or auto-generate missing values. |
| `--skip-dns-check` | Skip the public DNS verification. |
| `--skip-port-check` | Skip the local 80/443 port check. |
| `-h`, `--help` | Show all options. |

For an unattended install (a random admin password is generated and printed once
at the end):

```bash
sudo ./install.sh --domain panel.example.com --email you@example.com --non-interactive
```

When it finishes, the installer prints the panel URL and the admin credentials.

> **What the installer creates**
>
> - `/opt/nodexia` — application source and Compose files.
> - `/opt/nodexia/.env.production` — your configuration and secrets (`chmod 600`).
> - `nodexia.service` — systemd unit that builds and runs the Compose stack.
> - Docker volumes `nodexia_data` (SQLite database + pinned SSH host keys) and
>   `caddy_data` (TLS certificates). These persist across updates.

### Step 3 — Review and adjust the configuration

Most settings have safe defaults, but you can tune anything — and enable optional
features such as Telegram alerts — by editing the production environment file:

```bash
sudo nano /opt/nodexia/.env.production
```

The values you are most likely to change:

| Variable | What to set |
|----------|-------------|
| `NODEXIA_AUTH_USERNAME` / `NODEXIA_AUTH_PASSWORD` | The admin login. The password must **not** be a known-weak value such as `admin` or `change-this-password`; production startup refuses to boot otherwise. |
| `NODEXIA_SESSION_SECRET` | The HMAC key that signs session cookies. The installer generates a strong one; if you set it yourself it must be unique and at least 16 characters (`openssl rand -base64 48`). |
| `NODEXIA_TELEGRAM_BOT_TOKEN` | A Telegram bot token (from [@BotFather](https://t.me/BotFather)) to enable alert delivery. Leave blank to keep alerting disabled. Configure rules and channels in the panel under `/alerts`. |
| `NODEXIA_DOMAIN` | The public hostname. Changing it re-issues certificates on the next restart. |
| `NODEXIA_SCHEDULER_MONITORING_INTERVAL` | How often background monitoring runs (default `15m`). |
| `NODEXIA_DB_SQLITE_PATH` | SQLite database path inside the data volume. Leave as the default unless you know you need to change it. |

See [`.env.production.example`](.env.production.example) for every available
setting with inline notes, and [Configuration reference](#configuration-reference)
below for descriptions.

> **Never commit or share `.env.production`.** It contains your admin password,
> session secret, and (if set) Telegram bot token. It is excluded from Git by
> `.gitignore`.

### Step 4 — Apply configuration changes

The running containers read the configuration at startup, so restart the stack
after editing the file:

```bash
sudo systemctl restart nodexia
```

Equivalently, from the install directory:

```bash
cd /opt/nodexia
sudo docker compose -f compose.production.yml up -d
```

### Step 5 — Verify the deployment

```bash
sudo systemctl status nodexia                                   # service state
cd /opt/nodexia && sudo docker compose ps                       # container status
cd /opt/nodexia && sudo docker compose logs -f                  # live logs
curl -fsS https://panel.example.com/healthz                     # public health endpoint
```

Then open `https://panel.example.com` in a browser and sign in with the admin
account. If HTTPS is not reachable immediately, give Caddy a minute to obtain the
certificate, then retry.

### Updating to a new version

Pull the latest source and rerun the installer. Reruns rebuild the containers and
restart the stack while **preserving** the existing secrets in
`.env.production` (admin credentials, session secret, and Telegram token) unless
you explicitly pass replacements:

```bash
cd Nodexia
git pull
sudo ./install.sh --domain panel.example.com
```

### Manual production setup (without the installer)

On hosts where you would rather wire things up yourself (for example a non-Ubuntu
server that already has Docker), use the committed template and Compose file
directly:

```bash
# from a clone of the repository
cp .env.production.example .env.production
nano .env.production
#   - set NODEXIA_DOMAIN to your hostname
#   - set NODEXIA_AUTH_USERNAME / NODEXIA_AUTH_PASSWORD
#   - replace NODEXIA_SESSION_SECRET with: openssl rand -base64 48
#   - optionally set NODEXIA_TELEGRAM_BOT_TOKEN

docker compose -f compose.production.yml up -d --build
```

The Compose stack expects `.env.production` and `deploy/Caddyfile` (a default
Caddyfile is committed and works for a single domain). Point your domain's DNS at
the server and open ports 80/443 so Caddy can issue certificates.

---

## Configuration reference

Configuration is entirely environment-based. The full annotated list lives in
[`.env.example`](.env.example) (development) and
[`.env.production.example`](.env.production.example) (production). The settings
that matter most for a production deployment:

| Variable | Required in production | Description |
|----------|:----------------------:|-------------|
| `NODEXIA_AUTH_USERNAME` | yes | Admin login username. |
| `NODEXIA_AUTH_PASSWORD` | yes | Admin password. Rejected at startup if empty or a known-weak default. Repeated failed logins are rate limited. |
| `NODEXIA_SESSION_SECRET` | yes | HMAC key for signed session cookies. Must be unique and ≥ 16 characters. |
| `NODEXIA_DOMAIN` | yes | Public hostname served by Caddy. |
| `NODEXIA_SESSION_COOKIE_SECURE` | no | Marks cookies `Secure` (defaults to `true` in production / behind HTTPS). |
| `NODEXIA_BEHIND_REVERSE_PROXY` | no | `true` when running behind Caddy or another proxy. |
| `NODEXIA_AUTO_TLS` | no | `false` when the proxy (Caddy) terminates TLS. |
| `NODEXIA_TELEGRAM_BOT_TOKEN` | no | Telegram bot token for alert delivery. Blank disables sending. |
| `NODEXIA_DB_DRIVER` | no | `sqlite` (default) or `mysql`. |
| `NODEXIA_DB_SQLITE_PATH` | no | SQLite database path (persisted in the data volume). |
| `NODEXIA_DB_DSN` | with `mysql` | MySQL data source name when the driver is `mysql`. |
| `NODEXIA_SSH_HOST_KEY_POLICY` | no | `tofu` (trust on first use, default) or `insecure`. |
| `NODEXIA_SSH_KNOWN_HOSTS_PATH` | no | Where pinned SSH host keys are stored. |
| `NODEXIA_SCHEDULER_ENABLED` | no | Enable background monitoring/discovery jobs (default `true`). |
| `NODEXIA_SCHEDULER_MONITORING_INTERVAL` | no | Interval between monitoring runs (default `15m`). |
| `NODEXIA_SCHEDULER_NODES_INTERVAL` | no | Interval between node discovery runs. |
| `NODEXIA_HTTP_ADDR` | no | Internal listen address (default `:8080`, proxied by Caddy). |
| `NODEXIA_IMAGE_VERSION` | no | Image tag used by Compose builds. |

## Alerting

Nodexia can notify you over Telegram when a metric crosses a threshold:

1. Create a bot with [@BotFather](https://t.me/BotFather) and copy its token.
2. Set `NODEXIA_TELEGRAM_BOT_TOKEN` in `.env.production` and restart the stack.
3. In the panel, open **`/alerts`** to add a notification channel (your chat ID),
   define rules (metric, threshold, severity, cooldown), and send a test message.

If no token is configured, the alerts UI still works and records events — it just
shows a "not configured" notice instead of sending messages.

## Local development

For hacking on Nodexia locally you do not need Docker:

```bash
git clone https://github.com/Ho3einK84/Nodexia.git
cd Nodexia

cp .env.example .env
# Edit .env and set NODEXIA_AUTH_USERNAME and NODEXIA_AUTH_PASSWORD

go run ./cmd/nodexia/
```

Open `http://localhost:8080` and sign in with the configured admin account. In
development the session cookie is not marked `Secure`, so plain HTTP works.

## Important routes

| Path | Purpose |
|------|---------|
| `/` | Dashboard overview |
| `/servers` | Managed server registry |
| `/servers/{id}/monitoring` | Resource and traffic monitoring |
| `/servers/{id}/nodes` | Rebecca / Pasarguard node discovery |
| `/servers/{id}/system` | System facts |
| `/alerts` | Alert rules, channels, silences, and history |
| `/servers/{id}/terminal` | In-browser SSH terminal (xterm.js + WebSocket) |
| `/servers/{id}/commands` | Supporting SSH command runner |
| `/servers/{id}/files` | Supporting SFTP browser |
| `/servers/bulk` | Bulk reboot / update / delete across multiple servers |
| `/ops/diagnostics` | Operational diagnostics |
| `/healthz` | Health check |

## Build and test

```bash
make build   # build the binary into bin/nodexia
make test    # run the full test suite
go vet ./... # static analysis
```

## License

MIT
