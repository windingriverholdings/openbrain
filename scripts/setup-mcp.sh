#!/usr/bin/env bash
# OpenBrain: Register Go MCP server with Claude Code.
# Run this after 'make build' to point Claude Code at the Go binary.
# Safe to re-run — removes and re-adds the entry cleanly.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MCP_BIN="$REPO_DIR/bin/openbrain-mcp"
ENV_FILE="$REPO_DIR/.env"

if [[ ! -f "$MCP_BIN" ]]; then
    echo "Error: Go binary not found at $MCP_BIN"
    echo "Run 'make build' first."
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

# ── Read optional LLM config from .env ───────────────────────────────────────
EXTRACT_PROVIDER=$(grep '^OPENBRAIN_EXTRACT_PROVIDER=' "$ENV_FILE" 2>/dev/null | cut -d= -f2- | tr -d '"'"'" | xargs || echo "none")
OLLAMA_BASE_URL=$(grep '^OPENBRAIN_OLLAMA_BASE_URL=' "$ENV_FILE" 2>/dev/null | cut -d= -f2- | tr -d '"'"'" | xargs || echo "http://localhost:11434")

# ── Register via claude mcp add ──────────────────────────────────────────────
claude mcp remove openbrain 2>/dev/null || true

claude mcp add openbrain \
  --scope user \
  -e OPENBRAIN_DB_HOST=localhost \
  -e OPENBRAIN_DB_PORT=5432 \
  -e OPENBRAIN_DB_NAME=openbrain \
  -e OPENBRAIN_DB_USER=openbrain \
  -e OPENBRAIN_DB_PASSWORD="$DB_PASSWORD" \
  -e OPENBRAIN_EXTRACT_PROVIDER="$EXTRACT_PROVIDER" \
  -e OPENBRAIN_OLLAMA_BASE_URL="$OLLAMA_BASE_URL" \
  -- "$MCP_BIN"

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  OpenBrain (Go) registered with Claude Code."
echo ""
echo "  Binary: $MCP_BIN"
echo "  Verify: claude mcp list"
echo "  Then start a new Claude Code session — the tools"
echo "  will be available automatically."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
