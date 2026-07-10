#!/usr/bin/env bash
# Nodexia production installer.
#
# Safe to run again: a second run updates the source, refreshes generated
# service files, rebuilds the containers, and preserves existing credentials
# unless explicit replacements are provided.
set -euo pipefail

readonly DEFAULT_INSTALL_DIR="/opt/nodexia"
readonly DEFAULT_REPO_URL="https://github.com/Ho3einK84/Nodexia.git"
readonly DEFAULT_GIT_REF="main"
readonly DEFAULT_IMAGE_VERSION="v0.6.5"
readonly COMPOSE_FILE="compose.production.yml"
readonly ENV_FILE=".env.production"
readonly BUILD_ENV_FILE=".env"
readonly CADDYFILE_PATH="deploy/Caddyfile"
readonly SYSTEMD_UNIT="nodexia.service"
readonly CLI_PATH="/usr/local/bin/nodexia"
readonly CLI_DEFAULTS="/etc/default/nodexia"
# Prebuilt release binaries published by .github/workflows/release.yml. When a
# binary exists for the target version/arch the installer bakes it into a
# sub-second image (Dockerfile.prebuilt) instead of compiling from source.
readonly RELEASES_BASE="https://github.com/Ho3einK84/Nodexia/releases"
# GitHub API endpoint used to turn the "latest" alias into a concrete tag so the
# stored config and the version baked into the binary reference a real release.
readonly RELEASES_API="https://api.github.com/repos/Ho3einK84/Nodexia/releases/latest"

DOMAIN=""
ACME_EMAIL=""
INSTALL_DIR=""
REPO_URL=""
GIT_REF=""
IMAGE_VERSION=""
ADMIN_USER=""
ADMIN_PASSWORD=""
TELEGRAM_BOT_TOKEN=""
NON_INTERACTIVE=0
SKIP_DNS=0
SKIP_PORT_CHECK=0
FORCE_SOURCE=0
USE_PREBUILT=0

if [[ -t 1 ]]; then
  readonly RST=$'\033[0m'
  readonly BLD=$'\033[1m'
  readonly RED=$'\033[0;31m'
  readonly GRN=$'\033[0;32m'
  readonly YLW=$'\033[0;33m'
  readonly BLU=$'\033[0;34m'
  readonly CYN=$'\033[0;36m'
else
  readonly RST=""
  readonly BLD=""
  readonly RED=""
  readonly GRN=""
  readonly YLW=""
  readonly BLU=""
  readonly CYN=""
fi

say() { printf "%b\n" "$*"; }
info() { say "  ${BLU}->${RST} $*"; }
ok() { say "  ${GRN}OK${RST} $*"; }
warn() { say "  ${YLW}WARN${RST} $*"; }
err() { say "  ${RED}ERR${RST} $*"; }
die() { err "$*"; exit 1; }

trim() {
  local value="${1:-}"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf "%s" "$value"
}

banner() {
  say ""
  say "${CYN}${BLD}Nodexia Installer${RST}"
  say "${CYN}-----------------${RST}"
  say "Monitor Rebecca/Pasarguard nodes, traffic, and server resources."
  say ""
}

section() {
  say ""
  say "${BLD}$1${RST}"
  say "$(printf '%*s' "${#1}" '' | tr ' ' '-')"
}

usage() {
  cat <<EOF
Nodexia production installer

Usage:
  sudo ./install.sh [options]
  sudo ./scripts/install.sh [options]

Options:
  --domain <host>         Public hostname. Required unless an existing install has it.
  --email <address>       Optional ACME contact email for Caddy.
  --install-dir <path>    Install directory. Default: ${DEFAULT_INSTALL_DIR}
  --repo-url <url>        Git remote for fresh installs. Default: ${DEFAULT_REPO_URL}
  --git-ref <ref>         Branch or tag to deploy. Default: ${DEFAULT_GIT_REF}
  --image-version <tag>   Release/build version. Use a tag (e.g. v0.2.0) or
                          "latest" to pull a prebuilt binary. Default: ${DEFAULT_IMAGE_VERSION}
  --build-from-source     Always compile from source; skip prebuilt binaries.
  --admin-user <name>     Admin username. Preserved on rerun unless provided.
  --admin-password <pass> Admin password. Preserved on rerun unless provided.
  --telegram-bot-token <token>
                          Telegram bot token for alert delivery (optional).
                          Preserved on rerun unless provided.
  --non-interactive       Do not prompt; fail or generate missing values.
  --skip-dns-check        Skip public DNS verification.
  --skip-port-check       Skip local 80/443 port check.
  -h, --help              Show this help.

Examples:
  sudo ./install.sh --domain panel.example.com --email admin@example.com
  sudo ./install.sh --domain panel.example.com --non-interactive
  sudo ./install.sh

Rerun behavior:
  Running this installer again updates the app and restarts the stack while
  keeping .env.production secrets unless you pass new values.
EOF
}

require_value() {
  local option="$1"
  local value="${2:-}"
  [[ -n "$(trim "$value")" ]] || die "$option requires a value."
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
    --domain)
      require_value "$1" "${2:-}"
      DOMAIN="$(trim "$2")"
      shift 2
      ;;
    --email)
      require_value "$1" "${2:-}"
      ACME_EMAIL="$(trim "$2")"
      shift 2
      ;;
    --install-dir)
      require_value "$1" "${2:-}"
      INSTALL_DIR="$(trim "$2")"
      shift 2
      ;;
    --repo-url)
      require_value "$1" "${2:-}"
      REPO_URL="$(trim "$2")"
      shift 2
      ;;
    --git-ref)
      require_value "$1" "${2:-}"
      GIT_REF="$(trim "$2")"
      shift 2
      ;;
    --image-version)
      require_value "$1" "${2:-}"
      IMAGE_VERSION="$(trim "$2")"
      shift 2
      ;;
    --admin-user)
      require_value "$1" "${2:-}"
      ADMIN_USER="$(trim "$2")"
      shift 2
      ;;
    --admin-password)
      require_value "$1" "${2:-}"
      ADMIN_PASSWORD="$2"
      shift 2
      ;;
    --telegram-bot-token)
      require_value "$1" "${2:-}"
      TELEGRAM_BOT_TOKEN="$(trim "$2")"
      shift 2
      ;;
    --build-from-source)
      FORCE_SOURCE=1
      shift
      ;;
    --non-interactive)
      NON_INTERACTIVE=1
      shift
      ;;
    --skip-dns-check)
      SKIP_DNS=1
      shift
      ;;
    --skip-port-check)
      SKIP_PORT_CHECK=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      die "Unknown option: $1"
      ;;
    esac
  done
}

require_root() {
  if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    if command -v sudo >/dev/null 2>&1; then
      info "Re-running with sudo..."
      exec sudo -E bash "$0" "$@"
    fi
    die "Run this installer as root or with sudo."
  fi
}

env_path() {
  printf "%s/%s" "${INSTALL_DIR:-$DEFAULT_INSTALL_DIR}" "$ENV_FILE"
}

read_existing_env_value() {
  local key="$1"
  local file
  file="$(env_path)"
  [[ -f "$file" ]] || return 0
  grep -E "^${key}=" "$file" 2>/dev/null | tail -n1 | cut -d= -f2- | tr -d '\r' || true
}

set_defaults() {
  [[ -n "$INSTALL_DIR" ]] || INSTALL_DIR="$DEFAULT_INSTALL_DIR"
  [[ -n "$REPO_URL" ]] || REPO_URL="$DEFAULT_REPO_URL"
  [[ -n "$GIT_REF" ]] || GIT_REF="$DEFAULT_GIT_REF"

  # Default to this source tree's release version (DEFAULT_IMAGE_VERSION, bumped
  # per release) so rerunning the installer after `git pull` actually upgrades.
  # An explicit --image-version (e.g. "latest" or a tag) always wins. We do NOT
  # reuse the value stored in .env.production: a stale pin there is exactly what
  # would keep an updated install showing an old version.
  [[ -n "$IMAGE_VERSION" ]] || IMAGE_VERSION="$DEFAULT_IMAGE_VERSION"

  if [[ -z "$DOMAIN" ]]; then
    DOMAIN="$(read_existing_env_value NODEXIA_DOMAIN)"
    [[ -n "$DOMAIN" ]] && info "Using existing domain: $DOMAIN"
  fi

  if [[ -z "$ADMIN_USER" ]]; then
    ADMIN_USER="$(read_existing_env_value NODEXIA_AUTH_USERNAME)"
  fi

  if [[ -z "$ADMIN_PASSWORD" ]]; then
    ADMIN_PASSWORD="$(read_existing_env_value NODEXIA_AUTH_PASSWORD)"
  fi

  if [[ -z "$TELEGRAM_BOT_TOKEN" ]]; then
    TELEGRAM_BOT_TOKEN="$(read_existing_env_value NODEXIA_TELEGRAM_BOT_TOKEN)"
  fi
}

prompt_inputs() {
  if [[ -z "$DOMAIN" ]]; then
    if [[ "$NON_INTERACTIVE" -eq 1 ]]; then
      die "--domain is required for a new non-interactive install."
    fi
    read -r -p "  Public domain (example: panel.example.com): " DOMAIN
    DOMAIN="$(trim "$DOMAIN")"
  fi

  if [[ -z "$ACME_EMAIL" && "$NON_INTERACTIVE" -eq 0 ]]; then
    read -r -p "  ACME email (optional): " ACME_EMAIL
    ACME_EMAIL="$(trim "$ACME_EMAIL")"
  fi

  if [[ -z "$ADMIN_USER" ]]; then
    if [[ "$NON_INTERACTIVE" -eq 1 ]]; then
      ADMIN_USER="admin"
    else
      read -r -p "  Admin username (default: admin): " ADMIN_USER
      ADMIN_USER="$(trim "$ADMIN_USER")"
      [[ -n "$ADMIN_USER" ]] || ADMIN_USER="admin"
    fi
  fi

  if [[ -z "$ADMIN_PASSWORD" ]]; then
    if [[ "$NON_INTERACTIVE" -eq 1 ]]; then
      ADMIN_PASSWORD="$(random_string 24)"
      warn "Generated an admin password. It will be printed once at the end."
    else
      while [[ -z "$ADMIN_PASSWORD" ]]; do
        read -r -s -p "  Admin password: " ADMIN_PASSWORD
        say ""
        [[ -n "$ADMIN_PASSWORD" ]] || warn "Password cannot be empty."
      done
    fi
  fi
}

normalize_inputs() {
  DOMAIN="$(printf "%s" "$DOMAIN" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')"
  DOMAIN="${DOMAIN%.}"
  [[ -n "$DOMAIN" ]] || die "Domain cannot be empty."
  [[ "$DOMAIN" != *"/"* && "$DOMAIN" != *" "* ]] || die "Invalid domain: $DOMAIN"
  [[ -n "$ADMIN_USER" ]] || die "Admin username cannot be empty."
  [[ -n "$ADMIN_PASSWORD" ]] || die "Admin password cannot be empty."
}

random_string() {
  local length="${1:-32}"
  local bytes token
  bytes=$(((length + 1) / 2))
  if command -v openssl >/dev/null 2>&1; then
    token="$(openssl rand -hex "$bytes")"
  else
    token="$(od -An -N "$bytes" -tx1 /dev/urandom | tr -d ' \n')"
  fi
  printf "%s" "${token:0:length}"
}

preflight_ubuntu() {
  [[ -r /etc/os-release ]] || die "Cannot read /etc/os-release."
  # shellcheck disable=SC1091
  source /etc/os-release
  if [[ "${ID:-}" != "ubuntu" ]]; then
    die "Ubuntu is required. Detected: ${ID:-unknown}"
  fi
  if [[ "${VERSION_ID:-}" != "24.04" ]]; then
    warn "Designed for Ubuntu 24.04. Detected: ${VERSION_ID:-unknown}"
  else
    ok "Ubuntu ${VERSION_ID}"
  fi
}

ensure_packages() {
  local missing=()
  local cmd
  for cmd in git curl; do
    command -v "$cmd" >/dev/null 2>&1 || missing+=("$cmd")
  done
  if [[ "${#missing[@]}" -eq 0 ]]; then
    ok "Base packages are available"
    return 0
  fi
  info "Installing base packages: ${missing[*]}"
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${missing[@]}"
  ok "Base packages installed"
}

ensure_docker() {
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    ok "Docker and Compose are available"
    return 0
  fi

  info "Installing Docker and Compose..."
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq ca-certificates curl
  if ! command -v docker >/dev/null 2>&1; then
    curl -fsSL https://get.docker.com | sh
  fi
  if ! docker compose version >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq docker-compose-plugin
  fi
  systemctl enable --now docker >/dev/null 2>&1 || true
  ok "Docker is ready"
}

public_ipv4() {
  local ip=""
  ip="$(curl -4 -fsS --max-time 10 https://api.ipify.org 2>/dev/null || true)"
  if [[ -z "$ip" ]]; then
    ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  fi
  [[ -n "$ip" ]] || die "Could not determine this server public IPv4."
  printf "%s" "$ip"
}

preflight_dns() {
  if [[ "$SKIP_DNS" -eq 1 ]]; then
    warn "DNS check skipped"
    return 0
  fi

  local server_ip domain_ip
  server_ip="$(public_ipv4)"
  domain_ip="$(getent ahostsv4 "$DOMAIN" 2>/dev/null | awk '{print $1; exit}')"

  if [[ -z "$domain_ip" ]]; then
    die "DNS lookup failed for $DOMAIN. Point its A record to $server_ip."
  fi

  if [[ "$domain_ip" != "$server_ip" ]]; then
    die "$DOMAIN points to $domain_ip, but this server is $server_ip."
  fi
  ok "DNS verified: $DOMAIN -> $server_ip"
}

port_in_use() {
  local port="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -tlnH "sport = :$port" 2>/dev/null | grep -q .
    return $?
  fi
  return 1
}

preflight_ports() {
  if [[ "$SKIP_PORT_CHECK" -eq 1 ]]; then
    warn "Port check skipped"
    return 0
  fi

  local port
  for port in 80 443; do
    if port_in_use "$port"; then
      warn "Port $port is already in use; Caddy may fail to start."
    else
      ok "Port $port is free"
    fi
  done
}

prepare_install_dir() {
  if [[ -d "$INSTALL_DIR" ]]; then
    ok "Install directory exists: $INSTALL_DIR"
  else
    info "Creating install directory: $INSTALL_DIR"
    mkdir -p "$INSTALL_DIR"
    ok "Install directory created"
  fi
}

script_source_dir() {
  local dir
  dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [[ -f "$dir/../$COMPOSE_FILE" ]]; then
    cd "$dir/.." && pwd
  fi
}

git_update_install_dir() {
  cd "$INSTALL_DIR"
  info "Updating source from Git: $GIT_REF"
  git fetch --prune origin
  if git rev-parse --verify --quiet "origin/$GIT_REF" >/dev/null; then
    git checkout -B "$GIT_REF" "origin/$GIT_REF"
    git reset --hard "origin/$GIT_REF"
  else
    git fetch --tags origin
    git checkout -f "$GIT_REF"
  fi
  ok "Source updated"
}

sync_bundled_source() {
  local source_dir="$1"
  info "Syncing local source into $INSTALL_DIR"
  mkdir -p "$INSTALL_DIR"
  shopt -s dotglob nullglob
  local path base
  for path in "$source_dir"/*; do
    base="$(basename "$path")"
    case "$base" in
    .git | .env | "$ENV_FILE") continue ;;
    esac
    cp -a "$path" "$INSTALL_DIR/"
  done
  shopt -u dotglob nullglob
  ok "Local source synced"
}

ensure_source_tree() {
  local bundled
  bundled="$(script_source_dir || true)"

  if [[ -d "$INSTALL_DIR/.git" ]]; then
    git_update_install_dir
    return 0
  fi

  if [[ -n "$bundled" && "$(realpath "$bundled")" != "$(realpath "$INSTALL_DIR")" ]]; then
    sync_bundled_source "$bundled"
    return 0
  fi

  if [[ -n "$bundled" && -f "$INSTALL_DIR/$COMPOSE_FILE" ]]; then
    ok "Using local source tree: $INSTALL_DIR"
    return 0
  fi

  if [[ -f "$INSTALL_DIR/$COMPOSE_FILE" ]]; then
    ok "Using existing source tree: $INSTALL_DIR"
    return 0
  fi

  info "Cloning $REPO_URL ($GIT_REF)"
  rmdir "$INSTALL_DIR" 2>/dev/null || true
  git clone --branch "$GIT_REF" "$REPO_URL" "$INSTALL_DIR"
  ok "Source cloned"
}

generate_session_secret() {
  local existing
  existing="$(read_existing_env_value NODEXIA_SESSION_SECRET)"
  if [[ -n "$existing" && "$existing" != "change-this-production-secret" ]]; then
    printf "%s" "$existing"
  else
    random_string 64
  fi
}

write_env_production() {
  local secret env_file
  secret="$(generate_session_secret)"
  env_file="$(env_path)"

  cat >"$env_file" <<EOF
NODEXIA_IMAGE_VERSION=${IMAGE_VERSION}

NODEXIA_APP_NAME=Nodexia
NODEXIA_ENV=production
NODEXIA_LOG_LEVEL=info
NODEXIA_LOG_FORMAT=json
NODEXIA_HTTP_ADDR=:8080
NODEXIA_HTTP_READ_TIMEOUT=15s
NODEXIA_HTTP_WRITE_TIMEOUT=15s
NODEXIA_HTTP_IDLE_TIMEOUT=30s
NODEXIA_HTTP_SHUTDOWN_TIMEOUT=15s

NODEXIA_DB_DRIVER=sqlite
NODEXIA_DB_SQLITE_PATH=/var/lib/nodexia/nodexia.sqlite3
NODEXIA_DB_DSN=
NODEXIA_DB_MAX_OPEN_CONNS=10
NODEXIA_DB_MAX_IDLE_CONNS=5
NODEXIA_DB_CONN_MAX_LIFETIME=5m

NODEXIA_SSH_CONNECT_TIMEOUT=10s
NODEXIA_SSH_COMMAND_TIMEOUT=20s

NODEXIA_SCHEDULER_ENABLED=true
NODEXIA_SCHEDULER_STARTUP_DELAY=15s
NODEXIA_SCHEDULER_SWEEP_INTERVAL=1m
NODEXIA_SCHEDULER_MONITORING_INTERVAL=15m
NODEXIA_SCHEDULER_NODES_INTERVAL=30m
NODEXIA_SCHEDULER_RETRY_BACKOFF=3m
NODEXIA_SCHEDULER_CONNECT_TIMEOUT=10s
NODEXIA_SCHEDULER_COMMAND_TIMEOUT=45s

NODEXIA_SESSION_COOKIE_NAME=nodexia_session
NODEXIA_SESSION_SECRET=${secret}
NODEXIA_SESSION_TTL=12h
NODEXIA_SESSION_COOKIE_SECURE=true
NODEXIA_SSH_HOST_KEY_POLICY=tofu
NODEXIA_SSH_KNOWN_HOSTS_PATH=/var/lib/nodexia/ssh_known_hosts.json

NODEXIA_DOMAIN=${DOMAIN}
NODEXIA_AUTO_TLS=false
NODEXIA_BEHIND_REVERSE_PROXY=true

NODEXIA_AUTH_USERNAME=${ADMIN_USER}
NODEXIA_AUTH_PASSWORD=${ADMIN_PASSWORD}

# Telegram alerting (optional). Provide a bot token to enable alert delivery,
# then configure rules and channels in the panel under /alerts. Leave blank to
# keep sending disabled. This is a secret: never commit or share it.
NODEXIA_TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN}

NODEXIA_HEALTHCHECK_URL=http://127.0.0.1:8080/healthz
NODEXIA_HEALTHCHECK_TIMEOUT=3s
EOF

  chmod 600 "$env_file"
  ok "Configuration written: $env_file"
}

write_caddyfile() {
  local caddy_file="$INSTALL_DIR/$CADDYFILE_PATH"
  mkdir -p "$(dirname "$caddy_file")"

  if [[ -n "$ACME_EMAIL" ]]; then
    cat >"$caddy_file" <<EOF
{
	email ${ACME_EMAIL}
}

{\$NODEXIA_DOMAIN} {
	reverse_proxy app:8080
	encode zstd gzip
}
EOF
  else
    cat >"$caddy_file" <<'EOF'
{$NODEXIA_DOMAIN} {
	reverse_proxy app:8080
	encode zstd gzip
}
EOF
  fi
  ok "Caddyfile written"
}

write_systemd_unit() {
  local unit_path="/etc/systemd/system/$SYSTEMD_UNIT"
  cat >"$unit_path" <<EOF
[Unit]
Description=Nodexia Docker Compose stack
Documentation=https://github.com/Ho3einK84/Nodexia
After=docker.service network-online.target
Wants=network-online.target
Requires=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=${INSTALL_DIR}
Environment=COMPOSE_FILE=${COMPOSE_FILE}
ExecStart=/usr/bin/docker compose build
ExecStart=/usr/bin/docker compose up -d --remove-orphans
ExecStop=/usr/bin/docker compose down
TimeoutStartSec=0

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable "$SYSTEMD_UNIT" >/dev/null
  ok "Systemd unit installed: $SYSTEMD_UNIT"
}

target_arch() {
  local arch
  arch="$(dpkg --print-architecture 2>/dev/null || true)"
  if [[ -z "$arch" ]]; then
    case "$(uname -m)" in
    x86_64 | amd64) arch="amd64" ;;
    aarch64 | arm64) arch="arm64" ;;
    *) arch="" ;;
    esac
  fi
  printf "%s" "$arch"
}

# resolve_image_version turns the "latest" alias into the concrete release tag
# (e.g. v0.2.0) via the GitHub API. Without this, a "latest" source-build fallback
# would bake the literal string "latest" into the binary and show it as the panel
# version. Non-"latest" values are left untouched; on lookup failure we keep
# "latest" (the download URL still resolves) rather than abort.
resolve_image_version() {
  [[ "$IMAGE_VERSION" == "latest" ]] || return 0

  local tag
  tag="$(curl -fsSL --max-time 15 "$RELEASES_API" 2>/dev/null |
    sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
  if [[ -n "$tag" ]]; then
    info "Resolved latest release: ${tag}"
    IMAGE_VERSION="$tag"
  else
    warn "Could not resolve the latest release tag; using 'latest'."
  fi
}

# fetch_prebuilt_binary downloads the release binary for this host's
# architecture into $INSTALL_DIR/dist/nodexia and sets USE_PREBUILT=1 on success.
# Any failure (no release, offline, arch mismatch, bad checksum) is non-fatal:
# the caller falls back to building from source.
fetch_prebuilt_binary() {
  if [[ "$FORCE_SOURCE" -eq 1 ]]; then
    info "Building from source (--build-from-source)"
    return 1
  fi

  local arch asset base url checksums_url tmp expected actual
  arch="$(target_arch)"
  if [[ "$arch" != "amd64" && "$arch" != "arm64" ]]; then
    warn "No prebuilt binary for architecture '${arch:-unknown}'; building from source."
    return 1
  fi

  asset="nodexia_linux_${arch}"
  case "$IMAGE_VERSION" in
  dev | latest | "")
    base="${RELEASES_BASE}/latest/download" ;;
  *)
    base="${RELEASES_BASE}/download/${IMAGE_VERSION}" ;;
  esac
  url="${base}/${asset}"
  checksums_url="${base}/checksums.txt"

  tmp="$(mktemp -d)"
  info "Fetching prebuilt binary: ${asset} (${IMAGE_VERSION})"
  if ! curl -fL --retry 3 --connect-timeout 10 --max-time 180 -o "${tmp}/${asset}" "$url" 2>/dev/null; then
    warn "Prebuilt binary unavailable (${url}); building from source."
    rm -rf "$tmp"
    return 1
  fi

  if curl -fsSL --max-time 30 -o "${tmp}/checksums.txt" "$checksums_url" 2>/dev/null; then
    expected="$(grep -E "  ${asset}\$" "${tmp}/checksums.txt" 2>/dev/null | awk '{print $1}' | head -n1 || true)"
    actual="$(sha256sum "${tmp}/${asset}" | awk '{print $1}')"
    if [[ -z "$expected" || "$expected" != "$actual" ]]; then
      warn "Checksum verification failed for ${asset}; building from source."
      rm -rf "$tmp"
      return 1
    fi
    ok "Checksum verified"
  else
    warn "Checksums file unavailable; skipping verification."
  fi

  mkdir -p "${INSTALL_DIR}/dist"
  install -m 0755 "${tmp}/${asset}" "${INSTALL_DIR}/dist/nodexia"
  rm -rf "$tmp"
  USE_PREBUILT=1
  ok "Prebuilt binary ready (no source compile needed)"
  return 0
}

# write_build_env records the docker compose build-time variables in the project
# .env so both this run and later `docker compose build` invocations (systemd,
# the nodexia CLI) pick the same Dockerfile and version. It is interpolation-only
# and never injected into containers — runtime secrets stay in $ENV_FILE.
write_build_env() {
  local dockerfile env_file
  if [[ "$USE_PREBUILT" -eq 1 ]]; then
    dockerfile="Dockerfile.prebuilt"
  else
    dockerfile="Dockerfile"
  fi
  env_file="${INSTALL_DIR}/${BUILD_ENV_FILE}"
  cat >"$env_file" <<EOF
# Docker Compose build-time interpolation (read automatically from this
# directory). Managed by install.sh — runtime secrets live in ${ENV_FILE}.
NODEXIA_DOCKERFILE=${dockerfile}
NODEXIA_IMAGE_VERSION=${IMAGE_VERSION}
EOF
  ok "Build settings written: ${dockerfile} @ ${IMAGE_VERSION}"
}

# install_cli installs the `nodexia` management command and, for non-default
# install paths, records the directory so the CLI can find the stack.
install_cli() {
  local src="${INSTALL_DIR}/scripts/nodexia"
  if [[ ! -f "$src" ]]; then
    warn "CLI source not found ($src); skipping nodexia command install."
    return 0
  fi
  install -m 0755 "$src" "$CLI_PATH"
  if [[ "$INSTALL_DIR" != "$DEFAULT_INSTALL_DIR" ]]; then
    printf 'NODEXIA_DIR=%s\n' "$INSTALL_DIR" >"$CLI_DEFAULTS"
  else
    rm -f "$CLI_DEFAULTS" 2>/dev/null || true
  fi
  ok "CLI installed: nodexia (up, down, logs, update, status)"
}

deploy_stack() {
  cd "$INSTALL_DIR"
  [[ -f "$COMPOSE_FILE" ]] || die "Missing $INSTALL_DIR/$COMPOSE_FILE"
  export NODEXIA_IMAGE_VERSION="$IMAGE_VERSION"
  if [[ "$USE_PREBUILT" -eq 1 ]]; then
    export NODEXIA_DOCKERFILE="Dockerfile.prebuilt"
    info "Assembling image from prebuilt binary..."
  else
    export NODEXIA_DOCKERFILE="Dockerfile"
    info "Building image from source (first build may take ~100s)..."
  fi
  docker compose -f "$COMPOSE_FILE" build
  info "Starting stack..."
  docker compose -f "$COMPOSE_FILE" up -d --remove-orphans
  ok "Stack is running"
}

wait_for_health() {
  cd "$INSTALL_DIR"
  info "Waiting for app health..."
  local i
  for ((i = 1; i <= 40; i++)); do
    if docker compose -f "$COMPOSE_FILE" exec -T app /app/nodexia healthcheck >/dev/null 2>&1; then
      ok "Application healthcheck passed"
      return 0
    fi
    sleep 3
  done
  die "App did not become healthy. Run: cd $INSTALL_DIR && docker compose logs app"
}

verify_https() {
  local url="https://${DOMAIN}/healthz"
  info "Checking HTTPS endpoint..."
  local i
  for ((i = 1; i <= 24; i++)); do
    if curl -fsS --max-time 10 "$url" >/dev/null 2>&1; then
      ok "HTTPS is reachable: $url"
      return 0
    fi
    sleep 5
  done
  warn "HTTPS is not reachable yet. DNS or TLS may still be settling."
  warn "Manual check: curl -fsS $url"
}

print_summary() {
  local status="starting"
  if docker compose -f "$INSTALL_DIR/$COMPOSE_FILE" ps 2>/dev/null | grep -q "app.*Up"; then
    status="running"
  fi

  say ""
  say "${GRN}${BLD}Nodexia is ready${RST}"
  say ""
  say "  URL:       https://${DOMAIN}/"
  say "  Status:    ${status}"
  say "  Install:   ${INSTALL_DIR}"
  say "  Config:    ${INSTALL_DIR}/${ENV_FILE}"
  say ""
  say "  Admin"
  say "  -----"
  say "  Username:  ${ADMIN_USER}"
  say "  Password:  ${ADMIN_PASSWORD}"
  say ""
  say "  Manage with the nodexia command"
  say "  -------------------------------"
  say "  nodexia status     Show container status"
  say "  nodexia logs       Follow logs (add a service name to narrow)"
  say "  nodexia up         Start the stack"
  say "  nodexia down       Stop the stack"
  say "  nodexia restart    Restart the stack"
  say "  nodexia update     Pull the latest version and redeploy"
  say ""
  say "  (also: systemctl status ${SYSTEMD_UNIT})"
  say ""
  if [[ -z "$(trim "$TELEGRAM_BOT_TOKEN")" ]]; then
    say "  Tip: to enable Telegram alerts, set NODEXIA_TELEGRAM_BOT_TOKEN in"
    say "       ${ENV_FILE}, restart the stack, then configure /alerts."
    say ""
  fi
  say "  Edit config:  nano ${INSTALL_DIR}/${ENV_FILE}  (then: systemctl restart ${SYSTEMD_UNIT})"
  say "  Run this installer again to update Nodexia."
  say ""
}

main() {
  parse_args "$@"
  require_root "$@"
  set_defaults
  banner
  prompt_inputs
  normalize_inputs

  section "Plan"
  info "Domain: $DOMAIN"
  info "Install directory: $INSTALL_DIR"
  info "Git ref: $GIT_REF"
  info "Mode: install or update"

  section "Preflight"
  preflight_ubuntu
  ensure_packages
  prepare_install_dir
  preflight_dns
  preflight_ports
  ensure_docker

  section "Source"
  ensure_source_tree

  section "Binary"
  resolve_image_version
  fetch_prebuilt_binary || true
  write_build_env

  section "Configuration"
  write_env_production
  write_caddyfile
  write_systemd_unit
  install_cli

  section "Deploy"
  deploy_stack
  wait_for_health
  verify_https

  print_summary
}

main "$@"
