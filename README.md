# OpenBrain

Personal open-brain infrastructure — capture thoughts from any interface, retrieve them with semantic search.

**Stack:** PostgreSQL 16 + pgvector · fastembed (BAAI/bge-small-en-v1.5) · MCP Python server · pixi

All compute is local. No cloud dependencies.

## Quick Start

```bash
# 1. Set up the database
cp .env.example .env       # fill in DB password
bash scripts/setup-db.sh

# 2. Install dependencies
pixi install

# 3. Start the MCP server
pixi run server
```

## Prompt Kits

| # | Kit | Purpose |
|---|-----|---------|
| 1 | [Memory Migration](prompts/1_memory_migration.md) | Extract what your AI already knows about you |
| 2 | [Open Brain Spark](prompts/2_open_brain_spark.md) | Interview to discover your ideal workflow |
| 3 | [Quick Capture Templates](prompts/3_quick_capture.md) | Sentence starters for fast, structured capture |
| 4 | [Weekly Review](prompts/4_weekly_review.md) | End-of-week synthesis with clustering |

## MCP Tools

| Tool | Description |
|------|-------------|
| `capture_thought` | Save a thought with type, tags, and metadata |
| `search_thoughts` | Semantic RAG search over your knowledge base |
| `weekly_review` | Retrieve and group thoughts from the last N days |
| `brain_stats` | Aggregate stats about your knowledge base |
| `bulk_import` | Import multiple thoughts at once (memory migration) |

## Architecture

See [DECISIONS.md](DECISIONS.md) for all architectural decisions and rationale.

```
any interface (Telegram / Claude / web / CLI)
        │
        ▼
   MCP Server (Python)
        │
        ├── fastembed (BAAI/bge-small-en-v1.5) ── in-process ONNX
        │
        └── PostgreSQL 16
              ├── pgvector  (HNSW cosine search)
              ├── pg_trgm   (fuzzy full-text)
              └── TimescaleDB (time-series queries)
```
