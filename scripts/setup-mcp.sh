#!/usr/bin/env bash
# OpenBrain: Register as an MCP server with Claude Code.
# Run this on any machine after cloning the repo and running pixi install.
# Safe to re-run — only adds/updates the openbrain entry.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PYTHON="$REPO_DIR/.pixi/envs/default/bin/python"
CLAUDE_CONFIG="$HOME/.claude.json"
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

# ── Update ~/.claude.json ────────────────────────────────────────────────────
if [[ ! -f "$CLAUDE_CONFIG" ]]; then
    echo '{"mcpServers": {}}' > "$CLAUDE_CONFIG"
    echo "Created $CLAUDE_CONFIG"
fi

"$PYTHON" - <<PYEOF
import json

config_path = "$CLAUDE_CONFIG"
repo_dir    = "$REPO_DIR"
python      = "$PYTHON"
db_password = "$DB_PASSWORD"

with open(config_path) as f:
    config = json.load(f)

config.setdefault("mcpServers", {})["openbrain"] = {
    "command": python,
    "args": ["-m", "openbrain.server"],
    "env": {
        "PYTHONPATH": f"{repo_dir}/src",
        "OPENBRAIN_DB_HOST": "localhost",
        "OPENBRAIN_DB_PORT": "5432",
        "OPENBRAIN_DB_NAME": "openbrain",
        "OPENBRAIN_DB_USER": "openbrain",
        "OPENBRAIN_DB_PASSWORD": db_password,
    }
}

with open(config_path, "w") as f:
    json.dump(config, f, indent=2)

print(f"✓ OpenBrain registered in {config_path}")
PYEOF

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  OpenBrain is now available in Claude Code."
echo ""
echo "  Test it: start a new Claude Code session and ask:"
echo "    'Use the brain_stats tool to check my OpenBrain'"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
