#!/usr/bin/env bash
# Tier 2 Linux smoke check: launch the built desktop app under a minimal
# Xfce session and assert the tray icon registers as a StatusNotifierItem.
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

printf '#!/bin/sh\necho "[]"\n' >/tmp/devsy-cli-stub
chmod +x /tmp/devsy-cli-stub

dbus-run-session -- bash <<'INNER'
set -euo pipefail

ls /usr/lib/*/xfce4/panel/plugins/ 2>/dev/null | grep -i status \
    || echo "no statustray plugin file found"

# Give the panel a deterministic layout with the Status Tray plugin so it
# claims org.kde.StatusNotifierWatcher. The default layout may omit it.
export XDG_CONFIG_HOME=/tmp/xfce-config
mkdir -p "$XDG_CONFIG_HOME/xfce4/xfconf/xfce-perchannel-xml"
cat > "$XDG_CONFIG_HOME/xfce4/xfconf/xfce-perchannel-xml/xfce4-panel.xml" <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfce4-panel" version="1.0">
  <property name="configver" type="int" value="2"/>
  <property name="panels" type="array">
    <value type="int" value="1"/>
    <property name="panel-1" type="empty">
      <property name="position" type="string" value="p=6;x=0;y=0"/>
      <property name="length" type="uint" value="100"/>
      <property name="position-locked" type="bool" value="true"/>
      <property name="size" type="uint" value="30"/>
      <property name="plugin-ids" type="array">
        <value type="int" value="1"/>
      </property>
    </property>
  </property>
  <property name="plugins" type="empty">
    <property name="plugin-1" type="string" value="statustray"/>
  </property>
</channel>
XML

xfce4-panel &
PANEL_PID=$!

WATCHER=""
for i in $(seq 1 30); do
    if busctl --user list 2>/dev/null | grep -q 'org\.kde\.StatusNotifierWatcher'; then
        WATCHER=1
        echo "panel claimed org.kde.StatusNotifierWatcher after ${i}s"
        break
    fi
    sleep 1
done
if [ -z "$WATCHER" ]; then
    echo "WARN: panel did not claim org.kde.StatusNotifierWatcher within 30s"
fi

DEVSY_DISABLE_DAEMON=true DEVSY_CLI_PATH=/tmp/devsy-cli-stub npx electron . --no-sandbox &
APP_PID=$!
trap 'kill "$APP_PID" "$PANEL_PID" 2>/dev/null || true' EXIT

APP_OK=""
WATCH_OK=""
for i in $(seq 1 90); do
    NAMES=$(busctl --user list 2>/dev/null || true)
    if [ -z "$APP_OK" ] && printf '%s\n' "$NAMES" | grep -q 'StatusNotifierItem-'; then
        APP_OK=1
        echo "app owns a StatusNotifierItem name after ${i}s"
    fi
    if [ -n "$WATCHER" ]; then
        ITEMS=$(busctl --user get-property org.kde.StatusNotifierWatcher \
            /StatusNotifierWatcher org.kde.StatusNotifierWatcher \
            RegisteredStatusNotifierItems 2>/dev/null || true)
        COUNT=$(printf '%s' "$ITEMS" | awk '{print $2}')
        if [ -n "$COUNT" ] && [ "$COUNT" != "0" ]; then
            WATCH_OK=1
            echo "tray icon registered with the Xfce panel: $ITEMS"
            break
        fi
    fi
    if [ $((i % 15)) -eq 0 ]; then
        echo "poll ${i}s: app_sni=${APP_OK:-no} watcher=${WATCHER:-no} registered=${WATCH_OK:-no}"
    fi
    sleep 1
done

if [ -n "$WATCHER" ] && [ -z "$WATCH_OK" ]; then
    echo "FAIL: a StatusNotifierWatcher exists but the tray item never registered with it" >&2
    exit 1
fi
if [ -z "$APP_OK" ]; then
    echo "FAIL: the app never owned a StatusNotifierItem name" >&2
    exit 1
fi
if [ -z "$WATCHER" ]; then
    echo "NOTE: xfce4-panel did not claim org.kde.StatusNotifierWatcher;"
    echo "passing on app-side StatusNotifierItem registration alone."
fi
echo "tray smoke check passed"
INNER
