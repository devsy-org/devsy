#!/usr/bin/env bash
# Tier 2 Linux smoke check: launch the built desktop app under a minimal
# Xfce session (Xvfb + xfce4-panel) and assert the tray icon registers with
# the panel's StatusNotifier watcher.
set -euo pipefail

export DISPLAY="${DISPLAY:-:99}"

Xvfb "$DISPLAY" -screen 0 1280x800x24 &
XVFB_PID=$!
trap 'kill "$XVFB_PID" 2>/dev/null || true' EXIT

for _ in $(seq 1 50); do
    if xdpyinfo -display "$DISPLAY" >/dev/null 2>&1; then
        break
    fi
    sleep 0.2
done

dbus-run-session -- bash <<'INNER'
set -euo pipefail

xfce4-panel &
PANEL_PID=$!

DEVSY_DISABLE_DAEMON=true DEVSY_CLI_PATH=/bin/true npx electron . --no-sandbox &
APP_PID=$!
trap 'kill "$APP_PID" "$PANEL_PID" 2>/dev/null || true' EXIT

# The panel needs a moment to claim org.kde.StatusNotifierWatcher.
for _ in $(seq 1 90); do
    ITEMS=$(busctl --user get-property org.kde.StatusNotifierWatcher \
        /StatusNotifierWatcher org.kde.StatusNotifierWatcher \
        RegisteredStatusNotifierItems 2>/dev/null || true)
    COUNT=$(printf '%s' "$ITEMS" | awk '{print $2}')
    if [[ -n "$COUNT" && "$COUNT" != "0" ]]; then
        echo "tray icon registered with the Xfce panel: $ITEMS"
        exit 0
    fi
    sleep 1
done

echo "tray icon did not register with the Xfce panel" >&2
exit 1
INNER
