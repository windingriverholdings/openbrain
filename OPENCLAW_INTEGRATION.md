# OpenClaw Integration (personal branch)

This branch wires OpenBrain into OpenClaw via `mcporter`, giving the agent
semantic long-term memory accessible from Telegram, Discord, or any OpenClaw channel.

## Architecture

```
Telegram / Discord
      │
      ▼
  OpenClaw agent (gpt-5.3-codex)
      │
      ├── reads ~/.openclaw/workspace/OPENBRAIN.md  (knows when/how to use it)
      │
      └── mcporter call openbrain.<tool> [args]
                │
                ▼
          OpenBrain MCP server (local stdio)
                │
                └── PostgreSQL + pgvector (fully local)
```

## Files in this branch

| File | Purpose |
|------|---------|
| `deploy/OPENBRAIN.md` | Agent instructions — copied to `~/.openclaw/workspace/` by setup script |
| `scripts/setup-personal.sh` | One-shot: writes mcporter.json with credentials + installs OPENBRAIN.md |
| `scripts/setup-mcp.sh` | Register with Claude Code (run separately) |

## Setup (first time)

```bash
# 1. Database — reuse the existing .env (already configured)
bash scripts/setup-db.sh   # only needed on a new machine

# 2. Install pixi dependencies
pixi install

# 3. Personal/OpenClaw wiring — writes mcporter.json + installs OPENBRAIN.md
bash scripts/setup-personal.sh

# 4. Claude Code MCP registration
bash scripts/setup-mcp.sh

# 5. Restart OpenClaw so it picks up OPENBRAIN.md
openclaw gateway restart
```

## Usage via Telegram

Once running, just talk to OpenClaw naturally:

- **"Remember that I decided to use fastembed for local embeddings"**
  → agent calls `capture_thought`

- **"What do I know about the OpenBrain project?"**
  → agent calls `search_thoughts`

- **"Give me a weekly review"**
  → agent calls `weekly_review`, then synthesises

## Signal Filter

The agent (via OPENBRAIN.md instructions) acts as the intelligence layer.
It decides what's worth storing — not every message gets captured, only
meaningful decisions, insights, people, and events.
