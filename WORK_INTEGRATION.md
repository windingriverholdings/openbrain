# Work Integration (work branch)

Standalone OpenBrain — no OpenClaw required. Use this in work environments
where OpenClaw isn't present or appropriate.

## Architecture

```
Claude Code / any MCP client
      │
      └── OpenBrain MCP server (stdio)
                │
                └── PostgreSQL + pgvector (local)

CLI (direct):
  openbrain capture / search / review / stats / import
```

## Claude Code MCP Config

Add to `~/.claude.json` (or workspace `.claude/mcp.json`):

```json
{
  "mcpServers": {
    "openbrain": {
      "command": "/path/to/my-openbrain/.pixi/envs/default/bin/python",
      "args": ["-m", "openbrain.server"],
      "env": {
        "PYTHONPATH": "/path/to/my-openbrain/src"
      }
    }
  }
}
```

Then in any Claude Code session, you have full access to `capture_thought`,
`search_thoughts`, `weekly_review`, `brain_stats`, and `bulk_import`.

## CLI Usage (no MCP client needed)

```bash
# Capture a thought
pixi run python -m openbrain.cli capture "Decided to use Redis for session caching" \
  --type decision --tags work,backend

# Search
pixi run python -m openbrain.cli search "what did we decide about caching?"

# Weekly review
pixi run python -m openbrain.cli review --days 7

# Stats
pixi run python -m openbrain.cli stats

# Bulk import from JSON file
pixi run python -m openbrain.cli import thoughts.json
```

## Setup

```bash
cp .env.example .env    # configure DB credentials
bash scripts/setup-db.sh
pixi install
```

## Key Difference from personal branch

| | personal | work |
|--|---------|------|
| Interface | Telegram → OpenClaw → mcporter | Claude Code MCP / CLI |
| Agent filter | OpenClaw agent (gpt-5.3-codex) | Claude (via MCP) or manual CLI |
| Dependencies | mcporter + OpenClaw running | PostgreSQL only |
| Setup | OPENBRAIN.md in workspace | mcp.json config |
