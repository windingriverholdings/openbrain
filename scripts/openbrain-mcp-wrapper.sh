#!/usr/bin/env bash
# Wrapper that loads .env and launches the OpenBrain MCP server.
# Used by OpenFang and any MCP client that doesn't support env injection.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Export all vars from .env if it exists
if [ -f "$REPO_DIR/.env" ]; then
	set -a
	source "$REPO_DIR/.env"
	set +a
fi

exec "$REPO_DIR/bin/openbrain-mcp"
