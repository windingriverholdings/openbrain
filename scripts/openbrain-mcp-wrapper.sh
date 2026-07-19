#!/usr/bin/env bash
# Wrapper that loads config and launches the OpenBrain MCP server.
# Used by OpenFang and any MCP client that doesn't support env injection.
#
# Phase 3.5 (OB-066): config is loaded from /etc/openbrain/openbrain.env,
# the same authoritative source the four system services read, not the
# repo .env. The binary itself stays the repo bin/openbrain-mcp: it is
# dev-only and, per the Phase 2 locked decision, is never installed to
# /usr/local/bin, so this wrapper still needs the repo present for the
# binary even though its config no longer comes from there (a known,
# accepted seam; see Risk 4 in
# plans/plan-1-release-binary-deploy/phase-3.5-system-service-conversion.md).
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OPENBRAIN_MCP_ENV_FILE="${OPENBRAIN_MCP_ENV_FILE:-/etc/openbrain/openbrain.env}"

# Export all vars from the system config if it exists
if [ -f "$OPENBRAIN_MCP_ENV_FILE" ]; then
	set -a
	# shellcheck source=/dev/null
	source "$OPENBRAIN_MCP_ENV_FILE"
	set +a
fi

exec "$REPO_DIR/bin/openbrain-mcp"
