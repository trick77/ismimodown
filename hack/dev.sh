#!/bin/sh
# Local development. Runs the backend against a throwaway DB in /tmp.
#
# The Vite dev server lands in phase 5 alongside ui/; until then this is
# backend-only and http://127.0.0.1:8080 serves the placeholder shell.
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

cd "$ROOT/backend"
exec env \
  BACKEND_ADDR=127.0.0.1:8080 \
  BACKEND_PUBLIC_URL=http://127.0.0.1:8080 \
  BACKEND_DB_PATH="$DB_PATH" \
  BACKEND_LOG_LEVEL="${BACKEND_LOG_LEVEL:-debug}" \
  go run ./cmd/mimostats
