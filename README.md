# OpenBrain

> Your personal knowledge infrastructure. Capture thoughts from anywhere, retrieve them by meaning — forever.

**No cloud. No subscriptions. No data leaving your machine.**

---

## What Is This?

OpenBrain is a self-hosted semantic memory system built on PostgreSQL + pgvector. You talk to it naturally — through Telegram, a web chat interface, the CLI, or directly from Claude Code via MCP — and it stores your thoughts as vector embeddings. When you need to recall something, you ask in plain English and it finds the most relevant things you've ever captured, ranked by meaning rather than keywords.

It's the missing long-term memory layer for your AI-assisted life.

```
"What did I decide about the API architecture?"
"Who is Sarah Chen and what do I know about her?"
"Give me a weekly review — what happened this week?"
```

---

## Architecture

```
Telegram Bot ─────────────────────────────┐
Web Chat (mybrain.local:10203) ───────────┤
CLI (openbrain capture / search) ─────────┼──▶  intent.py (regex classifier)
Claude Code (MCP tools) ──────────────────┤         │
OpenClaw agent (file bridge) ─────────────┘         ▼
                                               brain.py (dispatcher)
                                                    │
                                    ┌───────────────┴───────────────┐
                                    ▼                               ▼
                             fastembed                        PostgreSQL 16
                        BAAI/bge-small-en-v1.5                + pgvector
                        (in-process ONNX, CPU)                + pg_trgm
                        384 dims, ~130MB                      + TimescaleDB
                        no daemon required                    HNSW cosine index
```

All compute is local. Embeddings are generated in-process via ONNX — no Ollama daemon, no cloud API.

---

## Features

- **Semantic search** — find thoughts by meaning, not just keywords (HNSW cosine similarity)
- **Typed thoughts** — decisions, insights, people, meetings, ideas, notes, memories
- **Multi-interface** — Telegram bot, web UI, CLI, MCP server, file bridge for sandboxed agents
- **Intent classification** — regex-based NL parser routes messages without an LLM
- **Weekly review** — time-grouped summaries across any date range
- **Bulk import** — migrate memories from any AI in one shot (see prompt kits)
- **Systemd daemons** — web and Telegram run as user services with auto-restart
- **Fully private** — PostgreSQL on bare metal, embeddings in-process, zero telemetry

---

## Stack

| Layer | Choice | Why |
|---|---|---|
| Database | PostgreSQL 16 + pgvector | Battle-tested, HNSW ANN search, full-text fallback |
| Embeddings | fastembed + bge-small-en-v1.5 | In-process ONNX, top MTEB score for its size, no daemon |
| MCP server | Python + `mcp` SDK | First-class Claude Code integration |
| Web server | FastAPI + WebSockets | Async, minimal, real-time chat UI |
| Telegram | python-telegram-bot | Single-user, token stays local |
| Package mgr | pixi | Reproducible environments, single lockfile |
| Deployment | systemd user services | No Docker overhead, native process supervision |

Full decision log with rationale: [DECISIONS.md](DECISIONS.md)

---

## Quick Start

### Prerequisites

- PostgreSQL 16 with `pgvector` and `pg_trgm` extensions
- [pixi](https://pixi.sh) installed

### Install

```bash
git clone https://github.com/windingriverholdings/my-openbrain
cd my-openbrain

# 1. Configure
cp .env.example .env
# Edit .env — set OPENBRAIN_DB_PASSWORD at minimum

# 2. Create the database and run migrations
bash scripts/setup-db.sh

# 3. Install Python dependencies (downloads fastembed model on first run ~130MB)
pixi install

# 4. Register with Claude Code as an MCP server
bash scripts/setup-mcp.sh
```

### Run the web UI

```bash
pixi run web
# Open http://mybrain.local:10203
```

Add `mybrain.local` to `/etc/hosts` if not already there:
```
127.0.0.1  mybrain.local
```

### Run the Telegram bot

Set `OPENBRAIN_TELEGRAM_BOT_TOKEN` and `OPENBRAIN_TELEGRAM_ALLOWED_USER_ID` in `.env`, then:

```bash
pixi run telegram
```

### Install as system daemons

```bash
bash scripts/install-services.sh
# Enables openbrain-web and openbrain-telegram as systemd user services
```

---

## CLI Usage

```bash
# Capture a thought
pixi run openbrain capture "decided to use Redis for session caching" --type decision --tags work,backend

# Search your brain
pixi run openbrain search "what did I decide about caching?"

# Weekly review (last 7 days)
pixi run openbrain review --days 7

# Stats
pixi run openbrain stats

# Bulk import from JSON
pixi run openbrain import thoughts.json
```

---

## MCP Tools (Claude Code)

After running `setup-mcp.sh`, these tools are available in every Claude Code session:

| Tool | Description |
|---|---|
| `capture_thought` | Save a thought with type, tags, source, and optional summary |
| `search_thoughts` | Semantic RAG search — returns top-K by cosine similarity |
| `weekly_review` | Retrieve thoughts grouped by type for a date range |
| `brain_stats` | Total count, breakdown by type, oldest/newest |
| `bulk_import` | Batch-insert thoughts (use for memory migration) |

---

## Talking to It

OpenBrain understands natural language. Any of these work in the web UI or Telegram:

**Capture:**
```
decided to use Postgres over MySQL for artisanstation
realised that deploys on Fridays are always risky
met Jamie Chen — runs engineering at Acme, former Google
remember: the API rate limit is 1000 req/min
```

**Search:**
```
what do I know about caching decisions?
find: deployment lessons
who is Jamie Chen?
```

**Review / stats:**
```
weekly review
what happened this week?
stats
how many thoughts?
```

Anything that looks like a statement gets captured. Anything that looks like a question triggers a search.

---

## Prompt Kits

Four prompt kits to get you started — copy and paste into any AI:

| Kit | Purpose |
|---|---|
| [1 — Memory Migration](prompts/1_memory_migration.md) | Extract what your AI already knows about you and import it |
| [2 — Open Brain Spark](prompts/2_open_brain_spark.md) | Interview to discover your ideal capture workflow |
| [3 — Quick Capture Templates](prompts/3_quick_capture.md) | Sentence starters for fast, structured capture |
| [4 — Weekly Review](prompts/4_weekly_review.md) | End-of-week synthesis with clustering |

---

## Database Schema

```sql
CREATE TYPE thought_type AS ENUM (
    'decision', 'insight', 'person', 'meeting', 'idea', 'note', 'memory'
);

CREATE TABLE thoughts (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    content      TEXT NOT NULL,
    summary      TEXT,
    embedding    vector(384) NOT NULL,   -- HNSW cosine index
    thought_type thought_type NOT NULL DEFAULT 'note',
    tags         TEXT[] DEFAULT '{}',
    source       VARCHAR(64) DEFAULT 'unknown',
    metadata     JSONB DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

---

## Branches

| Branch | Purpose |
|---|---|
| `work` | Standalone — Telegram bot, web UI, CLI, MCP server. No external agent dependencies. |
| `personal` | Everything in `work` plus OpenClaw integration via file bridge for sandboxed agent environments. |

---

## Extending

- **Add a Tailscale tunnel** — see `deploy/caddy-tailscale.conf` (pre-written, just fill in your hostname)
- **Add more thought types** — extend the `thought_type` enum in `sql/002_schema.sql`
- **Replace regex intent parsing** — swap `intent.py` with an LLM classifier (TODO in the file)
- **HTTP REST API** — already available at `/api/search`, `/api/capture`, `/api/stats`, `/api/review`

---

## Privacy

- All data stays on your machine
- Embeddings generated locally via ONNX (no API calls)
- Telegram bot token never leaves your `.env`
- PostgreSQL runs on localhost only (configurable)
- The web UI binds to `127.0.0.1` by default

---

## License

MIT
