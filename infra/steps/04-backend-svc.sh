#!/usr/bin/env bash
# Backend systemd unit: render, enable, start (or restart on re-run).
# Includes a post-start health check that fails loudly if the binary doesn't
# respond on its loopback port.
#
# Also handles UFW (opens 80/443 if the firewall is active).
#
# Expects from caller:
#   - log / ok / err helpers
#   - $INFRA_DIR, $INSTALL_DIR, $HOSTNAME, $SERVICE_PORT
#   - $BROWSER_STATE_DIR, $BROWSER_SECRET_FILE
set -euo pipefail

SERVICE_NAME="remote.futrx.service"
SERVICE_UNIT_PATH="/etc/systemd/system/$SERVICE_NAME"
BROWSER_SERVICE_NAME="remote.futrx-browser.service"
BROWSER_SERVICE_UNIT_PATH="/etc/systemd/system/$BROWSER_SERVICE_NAME"
LEGACY_SERVICE_NAME="remote.futrx.dev.service"
LEGACY_SERVICE_UNIT_PATH="/etc/systemd/system/$LEGACY_SERVICE_NAME"
HOST_CLI_PROFILE_PATH="/etc/profile.d/remote-futrx-host-clis.sh"

# shellcheck source=../lib/install-migration.sh
. "$INFRA_DIR/lib/install-migration.sh"
# shellcheck source=../lib/health-check.sh
. "$INFRA_DIR/lib/health-check.sh"

if [ -z "${LXD_BRIDGE_IP:-}" ]; then
    err "Cannot configure the shared browser broker without the LXD bridge IPv4 address."
    exit 1
fi

# ───────────────── browser broker ─────────────────
log "Configuring shared browser broker"
install -d -o remote-browser -g remote-browser -m 0700 "$BROWSER_STATE_DIR"
install -d -o root -g remote-browser -m 0750 "$(dirname "$BROWSER_SECRET_FILE")"
if [ ! -s "$BROWSER_SECRET_FILE" ]; then
    umask 0077
    head -c 48 /dev/urandom | base64 > "$BROWSER_SECRET_FILE"
fi
chown root:remote-browser "$BROWSER_SECRET_FILE"
chmod 0640 "$BROWSER_SECRET_FILE"
render_template "${INFRA_DIR}/templates/remote.futrx-browser.service.tmpl" \
                "$BROWSER_SERVICE_UNIT_PATH"
systemctl daemon-reload
if systemctl is-active --quiet "$BROWSER_SERVICE_NAME"; then
    systemctl restart "$BROWSER_SERVICE_NAME"
else
    systemctl enable --now "$BROWSER_SERVICE_NAME"
fi
if ! wait_for_http_health "http://${LXD_BRIDGE_IP}:9323/health" 30; then
    err "$BROWSER_SERVICE_NAME did not become healthy"
    journalctl -u "$BROWSER_SERVICE_NAME" -n 50 --no-pager >&2 || true
    exit 1
fi
ok "shared browser broker responding"

# Stop browser processes left running by a legacy or rolled-back release.
# Profiles stay on durable workspace mounts, so this is safe and idempotent.
log "Stopping legacy per-container browser stacks"
while IFS= read -r container_name; do
    [ -n "$container_name" ] || continue
    lxc exec "$container_name" -- sh -c \
        'if [ -x /workspace/.browser-gui/gui-up.sh ]; then /workspace/.browser-gui/gui-up.sh stop; fi' \
        >/dev/null 2>&1 || warn "Could not stop legacy browser in $container_name"
done < <(lxc list status=running -c n --format csv)

# ───────────────── systemd unit ─────────────────
log "Rendering $HOST_CLI_PROFILE_PATH"
render_template "${INFRA_DIR}/templates/remote-futrx-host-clis.sh.tmpl" \
                "$HOST_CLI_PROFILE_PATH"
chmod 0644 "$HOST_CLI_PROFILE_PATH"

log "Rendering $SERVICE_UNIT_PATH"
render_template "${INFRA_DIR}/templates/remote.futrx.service.tmpl" \
                "$SERVICE_UNIT_PATH"
systemctl daemon-reload

if ! prepare_legacy_service_migration "$LEGACY_SERVICE_NAME" "$LEGACY_SERVICE_UNIT_PATH"; then
    err "Could not pause $LEGACY_SERVICE_NAME; the existing service was left in place."
    exit 1
fi

if systemctl is-active --quiet "$SERVICE_NAME"; then
    log "Restarting $SERVICE_NAME"
    SERVICE_ACTION="restart"
else
    log "Starting $SERVICE_NAME"
    SERVICE_ACTION="enable"
fi
if { [ "$SERVICE_ACTION" = "restart" ] && ! systemctl restart "$SERVICE_NAME"; } || \
   { [ "$SERVICE_ACTION" = "enable" ] && ! systemctl enable --now "$SERVICE_NAME"; }; then
    err "$SERVICE_NAME failed to $SERVICE_ACTION."
    if ! rollback_legacy_service_migration "$SERVICE_NAME" "$LEGACY_SERVICE_NAME"; then
        err "Automatic rollback to $LEGACY_SERVICE_NAME also failed."
    fi
    journalctl -u "$SERVICE_NAME" -n 30 --no-pager >&2 || true
    exit 1
fi

# ───────────────── health check ─────────────────
if ! systemctl is-active --quiet "$SERVICE_NAME"; then
    err "Service failed to start. Recent logs:"
    if ! rollback_legacy_service_migration "$SERVICE_NAME" "$LEGACY_SERVICE_NAME"; then
        err "Automatic rollback to $LEGACY_SERVICE_NAME also failed."
    fi
    journalctl -u "$SERVICE_NAME" -n 30 --no-pager >&2 || true
    exit 1
fi

log "Health-checking backend on 127.0.0.1:${SERVICE_PORT}"
# Cold start binds the port only after converging container resource envelopes
# (~13s with 16 projects), so allow a real 30-second wall-clock deadline.
if ! wait_for_http_health "http://127.0.0.1:${SERVICE_PORT}/" 30; then
    err "Backend did not respond on 127.0.0.1:${SERVICE_PORT} within 30s"
    if ! rollback_legacy_service_migration "$SERVICE_NAME" "$LEGACY_SERVICE_NAME"; then
        err "Automatic rollback to $LEGACY_SERVICE_NAME also failed."
    fi
    journalctl -u "$SERVICE_NAME" -n 30 --no-pager >&2 || true
    exit 1
fi
ok "backend responding"

if ! complete_legacy_service_migration "$LEGACY_SERVICE_NAME" "$LEGACY_SERVICE_UNIT_PATH"; then
    err "Backend is healthy, but cleanup of $LEGACY_SERVICE_NAME failed."
    exit 1
fi

# ───────────────── UFW ─────────────────
if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
    log "Opening UFW for 80 + 443"
    ufw allow 80/tcp  >/dev/null || true
    ufw allow 443/tcp >/dev/null || true
    ufw allow in on "${LXD_BRIDGE:-lxdbr0}" to "${LXD_BRIDGE_IP}" port 9323 proto tcp >/dev/null || true
fi
