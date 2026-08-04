#!/bin/sh
# Local development: the Go daemon on :8080 and the Vite dev server on :5173,
# which proxies /api to the backend (see ui/vite.config.ts).
#
# Open http://127.0.0.1:5173 for hot reload, or :8080 to exercise the embedded
# bundle exactly as production serves it.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DB_PATH=${BACKEND_DB_PATH:-/tmp/mimostats-dev.db}

# The probe needs a real key to reach MiMo. Read it from .env if present rather
# than baking a placeholder in — .env is gitignored precisely because this is a
# live billable credential.
if [ -z "${BACKEND_MIMO_API_KEY:-}" ] && [ -f "$ROOT/.env" ]; then
  BACKEND_MIMO_API_KEY=$(grep -E '^BACKEND_MIMO_API_KEY=' "$ROOT/.env" | cut -d= -f2-)
  export BACKEND_MIMO_API_KEY
fi
if [ -z "${BACKEND_MIMO_API_KEY:-}" ]; then
  echo "hack/dev.sh: BACKEND_MIMO_API_KEY is unset and no .env carries it." >&2
  echo "  cp .env.example .env and fill it in, or export the key." >&2
  exit 1
fi

cleanup() {
  if [ -n "${BACKEND_PID:-}" ]; then
    kill "$BACKEND_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

(
  cd "$ROOT/backend"
  exec env \
    BACKEND_ADDR=127.0.0.1:8080 \
    BACKEND_PUBLIC_URL=http://127.0.0.1:8080 \
    BACKEND_DB_PATH="$DB_PATH" \
    BACKEND_LOG_LEVEL="${BACKEND_LOG_LEVEL:-debug}" \
    go run ./cmd/mimostats
) &
BACKEND_PID=$!

cd "$ROOT/ui"
npm run dev -- --host 127.0.0.1
