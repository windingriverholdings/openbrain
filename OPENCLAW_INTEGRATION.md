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

## Files Changed vs main

| File | Purpose |
|------|---------|
| `~/.mcporter/mcporter.json` | Registers OpenBrain as a system MCP server |
| `~/.openclaw/workspace/OPENBRAIN.md` | Teaches the agent when and how to use OpenBrain |

## Setup (first time)

```bash
# 1. Set up the database (from main)
cp .env.example .env
bash scripts/setup-db.sh

# 2. Install pixi dependencies (downloads fastembed model on first run)
pixi install

# 3. Verify mcporter can see OpenBrain
mcporter config list

# 4. Test the MCP connection
mcporter call openbrain.brain_stats

# 5. Restart the OpenClaw gateway so it picks up OPENBRAIN.md
openclaw gateway restart   # or restart the openclaw service
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
