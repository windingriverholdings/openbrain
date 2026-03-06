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

---

## Sandbox Security Model

OpenClaw runs the agent inside an isolated container sandbox. Understanding
what the sandbox can and cannot do is important for both security and debugging.

### What the sandbox has

| Capability | Available |
|---|---|
| Read/write files in `/workspace` | Yes |
| Run shell commands in the container | Yes (container builtins only) |
| OpenClaw session tools (spawn/list/send/status) | Yes |
| Host binaries (mcporter, curl, wget, python, git) | **No** |
| Network access to host localhost | **No** |
| Host filesystem outside `/workspace` | **No** |

### Why this is good for security

- A compromised agent prompt cannot execute arbitrary host processes
- A compromised agent cannot exfiltrate data via network calls
- OpenBrain credentials (DB password, bot tokens) never enter the sandbox
- The agent can only express intent through files — the host daemon validates and executes

### How the file bridge works

```
Sandbox (/workspace)                  Host (openbrain-watchd daemon)
─────────────────────                 ──────────────────────────────
Agent writes:                         Polls every 500ms for request file
openbrain-request.json                    │
{"cmd":"search","query":"..."}            │ picks it up
                                          │ calls OpenBrain directly
                                          │ (has DB credentials)
                                          │ writes response
Agent reads:                          openbrain-response.json
{"ok":true,"result":{...}}                │ deletes request file
```

### Credential isolation

```
.env (host only, never enters sandbox)
  └── openbrain-watchd reads at startup
        └── connects to PostgreSQL directly
              └── writes results to /workspace/openbrain-response.json
                    └── agent reads plain JSON — no credentials exposed
```

The agent sees results, never secrets.

### Bridge files

| File | Location | Purpose |
|---|---|---|
| `deploy/openbrain-sandbox` | copied to `/workspace/.local/bin/openbrain` | Agent-facing sh wrapper — writes request, waits for response |
| `scripts/openbrain-watchd.py` | runs on host via systemd | Polls for requests, dispatches to OpenBrain, writes responses |
| `deploy/openbrain-watchd.service` | `~/.config/systemd/user/` | Keeps watchd running, auto-restarts on failure |
| `deploy/OPENBRAIN.md` | copied to `/workspace/OPENBRAIN.md` | Tells the agent what OpenBrain is and how to call it |
