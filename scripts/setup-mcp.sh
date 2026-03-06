#!/usr/bin/env bash
# OpenBrain: Register as an MCP server with Claude Code.
# Run this on any machine after cloning the repo and running pixi install.
# Safe to re-run — removes and re-adds the entry cleanly.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PYTHON="$REPO_DIR/.pixi/envs/default/bin/python"
ENV_FILE="$REPO_DIR/.env"

if [[ ! -f "$PYTHON" ]]; then
    echo "Error: pixi environment not found. Run 'pixi install' first."
    exit 1
fi

if [[ ! -f "$ENV_FILE" ]]; then
    echo "Error: .env not found at $ENV_FILE"
    echo "Copy .env.example to .env and fill in your DB password first."
    exit 1
fi

# ── Read DB password from .env ───────────────────────────────────────────────
DB_PASSWORD=$(grep '^OPENBRAIN_DB_PASSWORD=' "$ENV_FILE" | cut -d= -f2- | tr -d '"'"'" | xargs)

if [[ -z "$DB_PASSWORD" || "$DB_PASSWORD" == "change_me" ]]; then
    echo "Error: OPENBRAIN_DB_PASSWORD is not set in .env"
    echo "Run 'bash scripts/setup-db.sh' first to generate a password."
    exit 1
fi

# ── Register via claude mcp add (handles permissions correctly) ──────────────
claude mcp remove openbrain 2>/dev/null || true

claude mcp add openbrain \
  --scope user \
  -e PYTHONPATH="$REPO_DIR/src" \
  -e OPENBRAIN_DB_HOST=localhost \
  -e OPENBRAIN_DB_PORT=5432 \
  -e OPENBRAIN_DB_NAME=openbrain \
  -e OPENBRAIN_DB_USER=openbrain \
  -e OPENBRAIN_DB_PASSWORD="$DB_PASSWORD" \
  -- "$PYTHON" -m openbrain.server

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  OpenBrain registered with Claude Code."
echo ""
echo "  Verify: claude mcp list"
echo "  Then start a new Claude Code session — the tools"
echo "  will be available automatically."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
