#!/usr/bin/env bash
# OpenBrain personal-branch setup: wires OpenBrain into OpenClaw via mcporter.
# Run this after setup-db.sh and pixi install.
# Safe to re-run — overwrites mcporter.json in place.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PYTHON="$REPO_DIR/.pixi/envs/default/bin/python"
ENV_FILE="$REPO_DIR/.env"
MCPORTER_JSON="${HOME}/.mcporter/mcporter.json"
OPENCLAW_WORKSPACE="${HOME}/.openclaw/workspace"

# ── Preflight ─────────────────────────────────────────────────────────────────
if [[ ! -f "$PYTHON" ]]; then
    echo "Error: pixi environment not found. Run 'pixi install' first."
    exit 1
fi

if [[ ! -f "$ENV_FILE" ]]; then
    echo "Error: .env not found. Copy .env.example to .env and fill in values."
    exit 1
fi

# ── Read credentials from .env ────────────────────────────────────────────────
_read_env() {
    grep "^${1}=" "$ENV_FILE" | cut -d= -f2- | tr -d '"'"'" | xargs
}

DB_PASSWORD=$(_read_env OPENBRAIN_DB_PASSWORD)
DB_HOST=$(_read_env OPENBRAIN_DB_HOST)
DB_PORT=$(_read_env OPENBRAIN_DB_PORT)
DB_NAME=$(_read_env OPENBRAIN_DB_NAME)
DB_USER=$(_read_env OPENBRAIN_DB_USER)

DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-5432}"
DB_NAME="${DB_NAME:-openbrain}"
DB_USER="${DB_USER:-openbrain}"

if [[ -z "$DB_PASSWORD" || "$DB_PASSWORD" == "change_me" ]]; then
    echo "Error: OPENBRAIN_DB_PASSWORD is not set in .env"
    echo "Run 'bash scripts/setup-db.sh' first."
    exit 1
fi

# ── mcporter: register OpenBrain with credentials ────────────────────────────
mkdir -p "$(dirname "$MCPORTER_JSON")"

cat > "$MCPORTER_JSON" <<MCPORTER
{
  "mcpServers": {
    "openbrain": {
      "command": "${PYTHON}",
      "args": ["-m", "openbrain.server"],
      "env": {
        "PYTHONPATH": "${REPO_DIR}/src",
        "OPENBRAIN_DB_HOST": "${DB_HOST}",
        "OPENBRAIN_DB_PORT": "${DB_PORT}",
        "OPENBRAIN_DB_NAME": "${DB_NAME}",
        "OPENBRAIN_DB_USER": "${DB_USER}",
        "OPENBRAIN_DB_PASSWORD": "${DB_PASSWORD}"
      },
      "description": "OpenBrain personal knowledge base — capture and search thoughts via RAG"
    }
  },
  "imports": []
}
MCPORTER

echo "  mcporter.json written to $MCPORTER_JSON"

# ── OpenClaw workspace: install OPENBRAIN.md ──────────────────────────────────
if [[ -d "$OPENCLAW_WORKSPACE" ]]; then
    cp "$REPO_DIR/deploy/OPENBRAIN.md" "$OPENCLAW_WORKSPACE/OPENBRAIN.md"
    echo "  OPENBRAIN.md installed to $OPENCLAW_WORKSPACE/"
else
    echo "  Warning: $OPENCLAW_WORKSPACE not found — skipping OPENBRAIN.md install"
    echo "           Create the directory and copy deploy/OPENBRAIN.md manually."
fi

# ── Smoke test: can the MCP server start? ────────────────────────────────────
echo ""
echo "  Smoke-testing MCP server..."
if PYTHONPATH="$REPO_DIR/src" \
   OPENBRAIN_DB_HOST="$DB_HOST" \
   OPENBRAIN_DB_PORT="$DB_PORT" \
   OPENBRAIN_DB_NAME="$DB_NAME" \
   OPENBRAIN_DB_USER="$DB_USER" \
   OPENBRAIN_DB_PASSWORD="$DB_PASSWORD" \
   "$PYTHON" -c "import openbrain.server; print('  MCP server imports OK')"; then
    echo "  MCP server: OK"
else
    echo "  MCP server: FAILED — check pixi install and .env"
    exit 1
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  OpenBrain personal setup complete."
echo ""
echo "  Next steps:"
echo "    1. Restart the OpenClaw gateway to pick up OPENBRAIN.md:"
echo "         openclaw gateway restart   # or restart the openclaw service"
echo ""
echo "    2. Test via mcporter:"
echo "         mcporter call openbrain.brain_stats"
echo ""
echo "    3. Register with Claude Code too:"
echo "         bash scripts/setup-mcp.sh"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
