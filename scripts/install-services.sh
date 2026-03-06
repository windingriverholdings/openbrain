#!/usr/bin/env bash
# OpenBrain: Install systemd services and configure mybrain.local
# Run once after setup-db.sh.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
USER="${SUDO_USER:-$(whoami)}"
DEPLOY_DIR="$REPO_DIR/deploy"

echo "Installing OpenBrain services for user: $USER"
echo "Repo: $REPO_DIR"

# ── /etc/hosts — add mybrain.local ──────────────────────────────────────────
HOSTS_ENTRY="127.0.0.1  mybrain.local"

if grep -q "mybrain.local" /etc/hosts; then
    echo "✓ mybrain.local already in /etc/hosts"
else
    echo "$HOSTS_ENTRY" >> /etc/hosts
    echo "✓ Added mybrain.local → 127.0.0.1 to /etc/hosts"
fi

# ── systemd services ─────────────────────────────────────────────────────────
for svc in openbrain-web openbrain-telegram; do
    SVC_FILE="$DEPLOY_DIR/${svc}.service"
    TARGET="/etc/systemd/system/${svc}@.service"

    # Replace %i with actual username in a temp copy
    sed "s|%i|$USER|g" "$SVC_FILE" > /tmp/${svc}.service.tmp

    cp /tmp/${svc}.service.tmp "/etc/systemd/system/${svc}@.service"
    echo "✓ Installed /etc/systemd/system/${svc}@.service"
done

systemctl daemon-reload

# ── Enable and start ──────────────────────────────────────────────────────────
systemctl enable --now "openbrain-web@$USER"
echo "✓ openbrain-web started → http://mybrain.local:10203"

# Only start telegram bot if token is configured
if grep -q "OPENBRAIN_TELEGRAM_BOT_TOKEN=" "$REPO_DIR/.env" 2>/dev/null && \
   ! grep -q "OPENBRAIN_TELEGRAM_BOT_TOKEN=$" "$REPO_DIR/.env" 2>/dev/null && \
   ! grep -q "OPENBRAIN_TELEGRAM_BOT_TOKEN=your_token" "$REPO_DIR/.env" 2>/dev/null; then
    systemctl enable --now "openbrain-telegram@$USER"
    echo "✓ openbrain-telegram started"
else
    echo "⚠  OPENBRAIN_TELEGRAM_BOT_TOKEN not set — skipping telegram service."
    echo "   Set it in .env, then run: sudo systemctl enable --now openbrain-telegram@$USER"
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  OpenBrain is running at http://mybrain.local:10203"
echo ""
echo "  Useful commands:"
echo "    journalctl -u openbrain-web@$USER -f       # web logs"
echo "    journalctl -u openbrain-telegram@$USER -f  # telegram logs"
echo "    systemctl status openbrain-web@$USER        # service status"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# TODO(tailscale): To expose mybrain over Tailscale:
#   1. Install Caddy: apt install caddy
#   2. Copy deploy/caddy-tailscale.conf to /etc/caddy/Caddyfile
#   3. Update OPENBRAIN_WEB_HOST=0.0.0.0 in .env
#   4. sudo systemctl restart openbrain-web@$USER caddy
