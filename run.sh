#!/usr/bin/env bash
# Run TransitMonitor with env vars sourced from .env (gitignored).
#
# Behaviour mirrors Docker's `restart: unless-stopped`:
#   - If the binary exits with 0 (normal shutdown / Ctrl-C / SIGTERM) → stop.
#   - If the binary exits non-zero (crash / panic / failed exec) → wait 2s, restart.
#   - syscall.Exec (in-panel restart) replaces the process image in-place; the
#     loop is not involved because PID is preserved. But if the new binary fails
#     to start (bad download, permission, etc.) the exec returns non-zero, the
#     old shell resumes, and the loop restarts the (possibly rolled-back) binary.
#
# .env must contain (at minimum):
#   TRANSMONITOR_ENCRYPTION_KEY=<the key used when stations were added via the web UI>
#       — required to decrypt DB-persisted station credentials. Without it the
#         server starts with 0 stations (creds won't decrypt).
#   TRANSMONITOR_DASHBOARD_PUBLIC=1   OR   TRANSMONITOR_DASHBOARD_TOKEN=<secret>
#       — required for non-localhost (proxy/external) dashboard access.
#   TRANSMONITOR_DASHBOARD_ADDR=0.0.0.0:7421  (optional, to bind all interfaces)
#
# Rotate the key without losing creds:
#   ./transitmonitor -rotate-key -old-key <old> -new-key <new>
set -a
[ -f .env ] && . ./.env
set +a

while true; do
  ./transitmonitor -config config.yaml "$@"
  rc=$?
  # Exit 0 = graceful shutdown (Ctrl-C / SIGTERM / explicit stop) → don't restart.
  if [ "$rc" -eq 0 ]; then
    echo "[run.sh] transitmonitor exited 0 — stopped."
    break
  fi
  echo "[run.sh] transitmonitor exited $rc — restarting in 2s..."
  sleep 2
done
