#!/bin/sh
# TransitMonitor container entrypoint wrapper.
#
# Enables in-panel self-update (sub2api-style) by routing execution to the
# persisted binary under /data/bin when one exists, falling back to the
# image-baked binary under /app. Because /data is the only volume that
# survives container recreation, a swap of /data/bin/transitmonitor by the
# updater persists across `docker compose up -d --build` / pod restarts.
#
# TRANSMONITOR_WRAPPER=1 lets the in-process updater know the wrapper is in
# effect (so WrapperReady() returns true and the /system page enables Upgrade).
set -e
BIN=/data/bin/transitmonitor
export TRANSMONITOR_WRAPPER=1
if [ -x "$BIN" ]; then
  exec "$BIN" "$@"
fi
exec /app/transitmonitor "$@"
