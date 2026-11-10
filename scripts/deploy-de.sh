#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DE_HOST="root@5.75.203.95"
DE_DIR="/opt/nodexia"

cd "$REPO_DIR"

GIT_REV="$(git rev-parse --short HEAD 2>/dev/null || echo 'dev')"
BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo 'dev')"
VERSION="dev-${BRANCH}-${GIT_REV}"

echo "==> [1/4] Building Nodexia (${VERSION})..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 /usr/local/go/bin/go build \
  -trimpath \
  -ldflags="-s -w -X main.version=${VERSION}" \
  -o bin/nodexia ./cmd/nodexia

echo "==> [2/4] Uploading binary to DE (${DE_HOST}:${DE_DIR}/dist/nodexia)..."
ssh -o ConnectTimeout=8 "${DE_HOST}" "mkdir -p ${DE_DIR}/dist"
scp -q bin/nodexia "${DE_HOST}:${DE_DIR}/dist/nodexia"

echo "==> [3/4] Rebuilding container & restarting service on DE..."
ssh -o ConnectTimeout=8 "${DE_HOST}" "cd ${DE_DIR} && docker compose build app && docker compose up -d app"

echo "==> [4/4] Verifying health..."
sleep 3
HEALTH_STATUS="$(ssh -o ConnectTimeout=8 "${DE_HOST}" "curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/login || echo '000'")"

if [ "$HEALTH_STATUS" = "200" ]; then
  echo "✓ Successfully deployed ${VERSION} to DE (HTTP 200 OK)"
else
  echo "⚠️ Warning: HTTP healthcheck returned ${HEALTH_STATUS} (expected 200)"
  exit 1
fi
