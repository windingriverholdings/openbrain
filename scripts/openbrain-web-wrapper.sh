#!/usr/bin/env bash
# Wrapper that loads .env and launches the OpenBrain web + MCP HTTP server.
# Used by the com.openbrain.web launchd agent.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [ -f "$REPO_DIR/.env" ]; then
	set -a
	# shellcheck disable=SC1091
	source "$REPO_DIR/.env"
	set +a
fi

exec "$REPO_DIR/bin/openbrain-web"
