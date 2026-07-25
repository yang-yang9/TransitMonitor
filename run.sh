#!/usr/bin/env bash
# Run TransitMonitor with env vars sourced from .env (gitignored).
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
exec ./transitmonitor -config config.yaml "$@"
