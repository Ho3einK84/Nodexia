#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_DIR"

# Load optional gitignored local overrides (.env.deploy) without overriding env vars.
if [ -f ".env.deploy" ]; then
  while IFS='=' read -r key val || [ -n "$key" ]; do
    key="${key#"${key%%[![:space:]]*}"}"
    key="${key%"${key##*[![:space:]]}"}"
    case "$key" in
      ''|\#*) continue ;;
    esac
    val="${val%$'\r'}"
    val="${val#"${val%%[![:space:]]*}"}"
    val="${val%"${val##*[![:space:]]}"}"
    if [[ "$val" =~ ^\"(.*)\"$ ]] || [[ "$val" =~ ^\'(.*)\'$ ]]; then
      val="${BASH_REMATCH[1]}"
    fi
    case "$key" in
      NODEXIA_DEPLOY_HOST|DE_HOST|NODEXIA_DEPLOY_DIR|DE_DIR)
        if [ -z "${!key:-}" ]; then
          export "${key}=${val}"
        fi
        ;;
    esac
  done < ".env.deploy"
fi

DE_HOST="${NODEXIA_DEPLOY_HOST:-${DE_HOST:-}}"
DE_DIR="${NODEXIA_DEPLOY_DIR:-${DE_DIR:-/opt/nodexia}}"
GO_BIN="${GO_BIN:-$(command -v go || echo /usr/local/go/bin/go)}"

if [ -z "$DE_HOST" ]; then
  echo "deploy: target host is not set." >&2
  echo "Set NODEXIA_DEPLOY_HOST (or DE_HOST in a gitignored .env.deploy file), e.g.:" >&2
  echo "  NODEXIA_DEPLOY_HOST=user@your-server.example.com ./scripts/deploy-de.sh" >&2
  exit 1
fi

GIT_REV="$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo 'dev')"
EXACT_TAG="$(git describe --tags --exact-match --match 'v*' HEAD 2>/dev/null || true)"
LATEST_TAG="$(git describe --tags --abbrev=0 --match 'v*' HEAD 2>/dev/null || true)"
DIRTY=""
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
  DIRTY="-dirty"
fi

if [ -n "${1:-}" ]; then
  VERSION="$1"
elif [ -n "${NODEXIA_DEPLOY_VERSION:-}" ]; then
  VERSION="${NODEXIA_DEPLOY_VERSION}"
elif [ -n "$EXACT_TAG" ] && [ -z "$DIRTY" ]; then
  VERSION="$EXACT_TAG"
elif [ "$BRANCH" = "main" ] && [ -n "$LATEST_TAG" ] && [ -z "$DIRTY" ] && \
     git diff --quiet "${LATEST_TAG}..HEAD" -- cmd internal web schema.sql go.mod go.sum '*.go' 2>/dev/null; then
  # Pure docs/deploy commits on main after a release tag produce an identical binary to LATEST_TAG.
  VERSION="$LATEST_TAG"
elif [ "$BRANCH" = "dev" ]; then
  VERSION="dev-${GIT_REV}${DIRTY}"
else
  VERSION="${BRANCH}-${GIT_REV}${DIRTY}"
fi

echo "==> [1/4] Building Nodexia (${VERSION})..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "${GO_BIN}" build \
  -trimpath \
  -ldflags="-s -w -X main.version=${VERSION}" \
  -o bin/nodexia ./cmd/nodexia

echo "==> [2/4] Uploading binary to DE (${DE_HOST}:${DE_DIR}/dist/nodexia)..."
ssh -o ConnectTimeout=8 "${DE_HOST}" "mkdir -p ${DE_DIR}/dist"
scp -q bin/nodexia "${DE_HOST}:${DE_DIR}/dist/nodexia"

echo "==> [3/4] Rebuilding container & restarting service on DE..."
ssh -o ConnectTimeout=8 "${DE_HOST}" "cd ${DE_DIR} && docker compose build --build-arg VERSION='${VERSION}' app && docker compose up -d app"

echo "==> [4/4] Verifying health..."
sleep 3
HEALTH_STATUS="$(ssh -o ConnectTimeout=8 "${DE_HOST}" "curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/healthz/live || echo '000'")"

if [ "$HEALTH_STATUS" = "200" ]; then
  echo "✓ Successfully deployed ${VERSION} to DE (HTTP 200 OK)"
else
  echo "⚠️ Warning: HTTP healthcheck returned ${HEALTH_STATUS} (expected 200)"
  exit 1
fi
