#!/usr/bin/env bash
# dogfood-restart.sh — rebuild the torque binary, bounce the dogfood
# serve on :8991, re-enable the scheduler. Idempotent. Meant to be run
# from ~/Projects-apps/torque (or anywhere with a clean main).
#
# What it does not do (by design):
#   - Merge anything. Merge your branches first, then run this.
#   - Kill active agent subprocesses. If you run this while a run is
#     dispatched, the claude subprocess will be orphaned and the run
#     will be marked as abandoned on the next scheduler tick.
#
# Exit codes: 0 ok, 1 build failed, 2 serve did not come up.

set -euo pipefail

REPO_ROOT="${REPO_ROOT:-$HOME/Projects-apps/torque}"
DATA_DIR="${TORQUE_DATA_DIR:-$HOME/.torque/dogfood}"
PROFILES_PATH="${TORQUE_PROFILES_PATH:-$DATA_DIR/profiles.yaml}"
PORT="${TORQUE_HTTP_PORT:-8991}"
BIN="${TORQUE_BIN:-$HOME/go/bin/torque}"
API="http://127.0.0.1:${PORT}/api/v1"

cd "$REPO_ROOT"

echo "==> go install ./cmd/torque"
if ! go install ./cmd/torque; then
  echo "FATAL: build failed" >&2
  exit 1
fi

# Stop any serve bound to the dogfood port.
if PID=$(lsof -iTCP:"$PORT" -sTCP:LISTEN -n -P -t 2>/dev/null); then
  if [ -n "$PID" ]; then
    echo "==> stopping serve pid=$PID (port $PORT)"
    kill "$PID" 2>/dev/null || true
    # wait up to 5s for the socket to free
    for _ in $(seq 1 10); do
      if ! lsof -iTCP:"$PORT" -sTCP:LISTEN -n -P >/dev/null 2>&1; then break; fi
      sleep 0.5
    done
  fi
fi

# Re-launch with the same env the dogfood KB documents.
# Rebuild the GUI before launching so dist/ is current (cheap; no-op if unchanged).
if [ -d "$REPO_ROOT/apps/gui" ] && [ -f "$REPO_ROOT/apps/gui/package.json" ]; then
  echo "==> rebuilding apps/gui"
  ( cd "$REPO_ROOT/apps/gui" && npm run build >/dev/null 2>&1 ) || echo "WARN: gui build failed; serve will show a stale dist"
fi

echo "==> starting serve on :$PORT"
nohup env \
  TORQUE_DB_PATH="$DATA_DIR/torque.db" \
  TORQUE_HTTP_PORT="$PORT" \
  TORQUE_DATA_DIR="$DATA_DIR" \
  TORQUE_PROFILES_PATH="$PROFILES_PATH" \
  TORQUE_SCHED_ENABLED=true \
  TORQUE_SCHED_WORKERS="${TORQUE_SCHED_WORKERS:-2}" \
  TORQUE_SCHED_INTERVAL_SECONDS="${TORQUE_SCHED_INTERVAL_SECONDS:-10}" \
  TORQUE_POSTGRES_DSN="" \
  TORQUE_GUI_DIR="$REPO_ROOT/apps/gui" \
  "$BIN" serve > "$DATA_DIR/serve.log" 2>&1 &
disown

# Wait for API.
for _ in $(seq 1 25); do
  if curl -fs "$API/scheduler/status" >/dev/null 2>&1; then
    echo "==> serve up on :$PORT"
    curl -fs "$API/scheduler/status" | python3 -m json.tool 2>/dev/null || true
    exit 0
  fi
  sleep 0.2
done
echo "FATAL: serve did not come up on :$PORT after 5s" >&2
tail -20 "$DATA_DIR/serve.log" >&2 || true
exit 2
