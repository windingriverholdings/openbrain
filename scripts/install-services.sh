#!/usr/bin/env bash
# OpenBrain: Install systemd USER services (Go binaries)
#
# All daemons run as systemd --user services:
#   - No root required for the daemons themselves
#   - Services run as YOU, not as root or a system account
#   - Credentials in .env are only readable by your user
#
# The only steps requiring sudo: /etc/hosts entry + loginctl enable-linger

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEPLOY_DIR="$REPO_DIR/deploy"
USER_SYSTEMD_DIR="${HOME}/.config/systemd/user"

echo "Installing OpenBrain user services (Go)"
echo "Repo: $REPO_DIR"
echo "User: $(whoami)"
echo ""

# ── Build Go binaries ────────────────────────────────────────────────────────
echo "Building Go binaries..."
cd "$REPO_DIR"
make build
echo "  binaries built in $REPO_DIR/bin/"
echo ""

# ── /etc/hosts — add mybrain.local ───────────────────────────────────────────
HOSTS_ENTRY="127.0.0.1  mybrain.local"
if grep -q "mybrain.local" /etc/hosts 2>/dev/null; then
    echo "  mybrain.local already in /etc/hosts"
else
    echo "  Adding mybrain.local to /etc/hosts (requires sudo)..."
    echo "$HOSTS_ENTRY" | sudo tee -a /etc/hosts > /dev/null
    echo "  mybrain.local → 127.0.0.1 added"
fi

# ── Install user service units ────────────────────────────────────────────────
mkdir -p "$USER_SYSTEMD_DIR"

for svc in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    src="$DEPLOY_DIR/${svc}.service"
    if [[ ! -f "$src" ]]; then
        echo "  Warning: $src not found — skipping"
        continue
    fi
    cp "$src" "$USER_SYSTEMD_DIR/${svc}.service"
    echo "  installed ~/.config/systemd/user/${svc}.service"
done

systemctl --user daemon-reload
echo "  systemd user daemon reloaded"
echo ""

# ── Enable lingering so services survive logout and start at boot ─────────────
if loginctl show-user "$(whoami)" 2>/dev/null | grep -q "Linger=yes"; then
    echo "  Linger already enabled"
else
    echo "  Enabling linger for $(whoami) (requires sudo)..."
    sudo loginctl enable-linger "$(whoami)"
    echo "  Linger enabled — services will start at boot without login"
fi

# ── Enable and start web ──────────────────────────────────────────────────────
systemctl --user enable --now openbrain-web
echo "  openbrain-web started → http://mybrain.local:10203"

# ── Enable and start watchd (sandbox file bridge) ────────────────────────────
systemctl --user enable --now openbrain-watchd
echo "  openbrain-watchd started (sandbox file bridge)"

# ── Enable and start telegram (only if token is configured) ──────────────────
TOKEN=$(grep '^OPENBRAIN_TELEGRAM_BOT_TOKEN=' "$REPO_DIR/.env" 2>/dev/null | cut -d= -f2- | xargs)
if [[ -n "$TOKEN" && "$TOKEN" != "your_bot_token_here" ]]; then
    systemctl --user enable --now openbrain-telegram
    echo "  openbrain-telegram started"
else
    echo "  openbrain-telegram skipped — set OPENBRAIN_TELEGRAM_BOT_TOKEN in .env"
    echo "  Then run: systemctl --user enable --now openbrain-telegram"
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  OpenBrain (Go) is running at http://mybrain.local:10203"
echo ""
echo "  Manage services (no sudo needed):"
echo "    systemctl --user status openbrain-web"
echo "    systemctl --user restart openbrain-web"
echo "    journalctl --user -u openbrain-web -f"
echo ""
echo "  Rebuild after code changes:"
echo "    cd $REPO_DIR && make build"
echo "    systemctl --user restart openbrain-web"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
