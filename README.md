# OpenBrain

> Your personal knowledge infrastructure. Capture thoughts from anywhere, retrieve them by meaning, forever.

**No cloud required. No subscriptions. Your data stays on your machine.**

---

## What Is This?

OpenBrain is a self-hosted semantic memory system built on PostgreSQL + pgvector. You talk to it naturally, through the CLI, a web chat interface, or directly from Claude Code via MCP, and it stores your thoughts as vector embeddings. When you need to recall something, you ask in plain English and it finds the most relevant things you've ever captured, ranked by meaning rather than keywords.

It's the missing long-term memory layer for your AI-assisted life.

```
"What did I decide about the API architecture?"
"Who is Alice Smith and what do I know about her?"
"Give me a weekly review, what happened this week?"
```

---

## Architecture

```
Telegram Bot (not yet implemented) ────────┐
Slack Bot (not yet implemented) ───────────┤
Web Chat (mybrain.local:10203) ────────────┼──▶  internal/intent (regex classifier)
CLI (openbrain capture / search) ──────────┤              │
Claude Code (MCP over stdio) ──────────────┘              │
                                                          ▼
Folder watcher / ingest (PDF, DOCX, ──▶ internal/docparse ──▶  internal/brain (dispatcher)
  OCR, PPTX, XLSX)                       (structured pipeline,          │
                                          bypasses intent)              │
                                    ┌────────────────────────┬─────────┴────────┐
                                    ▼                        ▼
                              Ollama                   PostgreSQL 16
                        nomic-embed-text                + pgvector
                        768 dims, local daemon          + pg_trgm
                        (model swap is config-only)     HNSW cosine index
```

All six binaries are Go, built from a single module. Interactive input (web chat, CLI free text) is routed by the `internal/intent` regex classifier; document ingestion (the folder watcher and the `ingest_document` path) instead runs through the `internal/docparse` structured pipeline and does not touch the classifier. Embeddings run through a local Ollama daemon, not an in-process library, so swapping models is a config change plus `openbrain reembed` (see [Database Schema](#database-schema)).

---

## Features

- **Hybrid search**: semantic (HNSW cosine similarity) and keyword (native PostgreSQL full-text search, a weighted `tsvector` ranked with `ts_rank`) combined and weighted per query
- **Typed thoughts**: decisions, insights, people, meetings, ideas, notes, memories
- **Temporal fact tracking**: supersede an old thought when new knowledge replaces it, and query the timeline for any subject
- **Document ingestion**: PDF and DOCX built in; PPTX and XLSX via the `markitdown` CLI; OCR (Tesseract) available in source builds with `make build-ocr` (not included in the published release binaries, see Quick Start)
- **Folder watcher**: auto-ingest documents dropped into a watched directory
- **Multi-interface**: CLI, web chat, MCP (stdio for Claude Code, optional HTTP/SSE with OAuth for remote MCP clients)
- **Weekly review**: time-grouped summaries across any date range
- **Bulk import**: migrate memories from any AI in one shot (see prompt kits)
- **Systemd daemons**: the web server and any bots run as user services with auto-restart
- **Fully private by default**: PostgreSQL on bare metal, embeddings via a local Ollama daemon, zero telemetry (see [Privacy](#privacy) for the one opt-in exception)
- **Conversational search**: ask questions about your stored thoughts and receive a local-model answer grounded in retrieved notes with clickable citations. See [Conversational Search](docs/conversational-search.md) for the complete execution flow.

Telegram and Slack integration are scaffolded (config, systemd units, binaries) but the bots themselves are not yet implemented. Both `cmd/openbrain-telegram` and `cmd/openbrain-slack` currently log one placeholder line and exit 0.

---

## Stack

| Layer | Choice | Why |
|---|---|---|
| Language | Go | Single static binary per component, no runtime dependency tree |
| Database | PostgreSQL 16 + pgvector + pg_trgm | Battle-tested, HNSW ANN search, native full-text keyword search (`tsvector` + `ts_rank`) |
| Embeddings | Ollama + `nomic-embed-text` | Local daemon, 768 dims, 8192 token context, no cloud API |
| MCP server | Go, `mark3labs/mcp-go` | stdio transport for Claude Code; HTTP + SSE with OAuth 2.0 (authorization-code grant with S256 PKCE) available from `openbrain-web` |
| Web server | Go `net/http` + `gorilla/websocket` | Minimal, real-time chat UI, also hosts the REST API and optional MCP HTTP transport |
| Document parsing | Built-in (PDF, DOCX) + `markitdown` CLI (PPTX, XLSX); OCR (Tesseract) opt-in via `make build-ocr` | Native Go parsing where practical, external tool only where it earns its keep; OCR needs cgo + system tesseract, so it is not in the default build |
| Deployment | systemd user services | No Docker overhead, native process supervision |

Full decision log with rationale: [DECISIONS.md](DECISIONS.md). Several early entries there predate the Go rewrite and describe a superseded design: decision 002 (in-process `fastembed` embeddings, since replaced by the Ollama `nomic-embed-text` setup above), decision 003 (a Python MCP server, now Go), and decision 004 (the `pixi` package manager, no longer used). Treat the stack table above as the current source of truth.

---

## Quick Start

### Prerequisites (both install paths)

- PostgreSQL 16 with `pgvector` and `pg_trgm` extensions
- [Ollama](https://ollama.com) running locally, with `nomic-embed-text` pulled: `ollama pull nomic-embed-text`

Pick one of the two install paths below.

### Option A: Install from a release binary

Prebuilt binaries are published on every tagged release. This is the fastest path if you don't need to modify the source.

**1. Download.** Grab the binaries you need from the [latest release](https://github.com/windingriverholdings/openbrain/releases/latest). Six binaries are published for `linux/amd64` (the only platform published today; build from source for other platforms, see Option B):

| Binary | What it does |
|---|---|
| `openbrain` | CLI: capture, search, review, stats, import, reembed |
| `openbrain-mcp` | MCP server for Claude Code (stdio transport) |
| `openbrain-web` | Web chat UI, REST API, and optional HTTP/SSE MCP transport with OAuth |
| `openbrain-telegram` | Telegram bot (not yet implemented, placeholder binary) |
| `openbrain-slack` | Slack bot (not yet implemented, placeholder binary) |
| `openbrain-watchd` | Folder watcher daemon: auto-ingests documents dropped into a watched directory |

None of the six release binaries include OCR support: OCR requires cgo and the system `tesseract-ocr` library, so it is gated behind a build tag (`make build-ocr`) and left out of the `make dist` build that produces release assets. If you need OCR, build from source (Option B) with `make build-ocr` instead of `make build`.

A `SHA256SUMS` file is published alongside the binaries. Download it into the same directory and verify before running anything:

```bash
# Verify every binary at once
sha256sum -c SHA256SUMS

# Or verify just the one binary you downloaded
sha256sum -c --ignore-missing SHA256SUMS
```

**2. Install.** Make the binary executable, give it a clean name, and put it on `PATH`:

```bash
chmod +x openbrain-v0.4.0-linux-amd64
mv openbrain-v0.4.0-linux-amd64 openbrain
sudo mv openbrain /usr/local/bin/
```

`sudo` is only needed for the move into `/usr/local/bin`; skip it if you're installing to a directory you already own (e.g. `~/.local/bin`, provided it's on `PATH`). Repeat for each binary you plan to run.

**3. Verify.**

```bash
openbrain --version
```

This should print the release version (for example `v0.4.0`), stamped in at build time from the release tag. It exits immediately with no database or config required, so it works as a quick sanity check even before `.env` is set up.

**4. Get the setup files.** The release publishes only the six binaries and `SHA256SUMS`. The database schema, the setup scripts, and the config template all live in the repository, so a binary install still needs those files from the repo. The lightest way to get them without a full source build is a shallow clone:

```bash
git clone --depth 1 https://github.com/windingriverholdings/openbrain openbrain-setup
cd openbrain-setup
```

This gives you `.env.example`, the `sql/` migrations, and `scripts/setup-db.sh` / `scripts/setup-mcp.sh`. If you would rather not clone, fetch the individual files from the `main` branch over raw GitHub (`.env.example`, every file under `sql/`, and the two scripts under `scripts/`) into a matching directory layout; the scripts resolve `sql/` relative to their own location, so keep `scripts/` and `sql/` as siblings.

**5. Run setup.** From the directory holding the setup files: copy `.env.example` to `.env` and set `OPENBRAIN_DB_PASSWORD`, run `scripts/setup-db.sh` to create the database and apply migrations, then `scripts/setup-mcp.sh` to register `openbrain-mcp` with Claude Code. `setup-mcp.sh` looks for the MCP binary at `bin/openbrain-mcp` relative to the repo root; if you installed the prebuilt binary to `/usr/local/bin` instead, either symlink `bin/openbrain-mcp` to it or point `claude mcp add` at the installed path directly.

### Option B: Build from source

```bash
git clone https://github.com/windingriverholdings/openbrain
cd openbrain

# 1. Configure
cp .env.example .env
# Edit .env, set OPENBRAIN_DB_PASSWORD at minimum

# 2. Create the database and run migrations
bash scripts/setup-db.sh

# 3. Build all six binaries into bin/ (requires the Go toolchain, see go.mod for the version)
make build
# Or, for OCR support (requires tesseract-ocr + libtesseract-dev): make build-ocr

# 4. Register the MCP server with Claude Code
bash scripts/setup-mcp.sh
```

`make build` stamps the binary with the version from `git describe --tags --always`, falling back to the `dev` sentinel when no tag is reachable. `make install` (Go's `go install`) is also available for `openbrain`, `openbrain-mcp`, and `openbrain-web` if you'd rather they land in your `GOBIN`.

### Run the web UI

```bash
bin/openbrain-web
# Open http://mybrain.local:10203
```

Add `mybrain.local` to `/etc/hosts` if not already there:
```
127.0.0.1  mybrain.local
```

### Brain Map (`/graph`)

The web UI includes a brain map that visualizes all your thoughts as a 2D similarity cloud — thoughts near each other in the map are semantically related, cluster regions are labeled by an LLM, and edges connect the most similar pairs.

The map is precomputed offline. Run this whenever you want to refresh it (after capturing new thoughts, or on first use):

```bash
make viz
make build
```

Then visit `http://mybrain.local:10203/graph`, or click **🗺 Brain Map** in the chat header.

`make viz` reads the embeddings already stored in Postgres (no re-embedding), runs UMAP + HDBSCAN to project them to 2D, and calls your local Ollama instance to label each cluster using whichever of `OPENBRAIN_EXTRACT_MODEL_FAST`, `OPENBRAIN_EXTRACT_MODEL`, or `OPENBRAIN_CHAT_MODEL` is set in `.env` (checked in that order — at least one must be set or the script exits with an error). It writes the result to `cmd/openbrain-web/static/brain.json`; `make build` embeds that file into the binary. Takes roughly 30–60 seconds depending on how many thoughts you have.

**Requirements** (Python packages, install once):

```bash
pip install psycopg[binary] pgvector numpy umap-learn hdbscan scikit-learn python-dotenv requests
```

The map reads your active embedding model directly from Postgres, so it works with any model (`nomic-embed-text`, `qwen3-embedding`, etc.) without any config changes.

### Telegram and Slack bots

`openbrain-telegram` and `openbrain-slack` are scaffolded (config keys, systemd units) but not yet implemented: running either logs one placeholder line and exits 0. `.env.example` documents the token variables (`OPENBRAIN_TELEGRAM_BOT_TOKEN`, `OPENBRAIN_SLACK_BOT_TOKEN`, etc.) for when that lands.

### Install as system daemons

```bash
bash scripts/install-services.sh
# Copies systemd --user units for openbrain-web, openbrain-telegram, openbrain-slack, and openbrain-watchd
```

The script builds the binaries itself (`make build`) if they are not already present, then enables and starts `openbrain-web` and `openbrain-watchd`. It enables `openbrain-telegram` only when a real `OPENBRAIN_TELEGRAM_BOT_TOKEN` is set in `.env`. The `openbrain-slack` unit is copied but not enabled or started. Because the telegram and slack binaries are stubs that exit 0 (a clean exit, which `Restart=on-failure` does not restart), their units stay inactive even once enabled, until the bots are implemented.

---

## CLI Usage

```bash
# Capture a thought
openbrain capture "decided to use Redis for session caching"

# Search your brain
openbrain search "what did I decide about caching?"

# Weekly review
openbrain review

# Stats
openbrain stats

# Re-embed all thoughts with NULL embeddings (after an embedding model swap)
openbrain reembed

# Import from a JSON file
openbrain import thoughts.json

# Auto-classify and dispatch free text (capture vs. search vs. review, etc.)
openbrain "remember: the API rate limit is 1000 req/min"

# Print the build version and exit
openbrain --version
```

---

## MCP Tools (Claude Code)

After running `setup-mcp.sh`, these tools are available in every Claude Code session:

| Tool | Description |
|---|---|
| `capture_thought` | Capture a thought into OpenBrain |
| `search_thoughts` | Search OpenBrain for thoughts related to a query |
| `weekly_review` | Get a review of thoughts from the past N days |
| `brain_stats` | Return aggregate statistics about the OpenBrain knowledge base |
| `bulk_import` | Import multiple thoughts as one atomic batch; either every thought is saved or none is, and any failure returns an error rather than a partial summary |
| `supersede_thought` | Capture a new thought and mark an older one as superseded; the capture and the supersede are atomic |
| `thought_timeline` | Get the timeline of thoughts about a subject |
| `extract_thoughts` | Extract structured thoughts from long-form text using an LLM |
| `ingest_document` | Ingest a document (PDF, DOCX, or image via OCR) and optionally auto-capture it as thoughts |

`openbrain-mcp` serves all nine tools over stdio, the standard transport for a local Claude Code integration. `openbrain-web` additionally exposes an HTTP + SSE MCP transport with OAuth 2.0 (for remote clients such as Claude.ai's connector) when `OPENBRAIN_MCP_HTTP_ENABLED=true` is set; see `.env.example` for the required token and OAuth settings. The HTTP/SSE transport deliberately excludes `ingest_document` (it reads the local filesystem, which should not be reachable from a remote client), so a remote connection exposes eight tools, not nine.

### Remote MCP access (Host allowlist)

A typical deployment binds `openbrain-web` to loopback (`OPENBRAIN_WEB_HOST=127.0.0.1`) and fronts it with a reverse proxy such as cloudflared, which forwards the public hostname (for example `openbrain.wr-s.net`) in the `Host` header unchanged. The MCP transport (`mark3labs/mcp-go`) validates that header as DNS rebinding protection, so the public hostname must be explicitly allowed or every remote `/mcp` and `/sse` request is rejected with 403.

Set `OPENBRAIN_MCP_ALLOWED_HOSTS` to a comma-separated list of the hostnames remote clients will send, for example:

```
OPENBRAIN_MCP_ALLOWED_HOSTS=openbrain.wr-s.net
```

Loopback hosts (`localhost`, `127.0.0.1`, `::1`) are always permitted in addition to this list, so local tooling and health checks keep working with no configuration. See `.env.example` for the full entry.

---

## Talking to It

OpenBrain understands natural language via a regex classifier (`internal/intent`), no LLM required for routing. Any of these work in the web UI or CLI free-text form:

**Capture:**
```
decided to use Postgres over MySQL for the user service
realised that deploys on Fridays are always risky
met Bob Jones: runs engineering at Acme, former Google
remember: the API rate limit is 1000 req/min
```

**Supersede:**
```
actually, we switched from Redis to Valkey
update: Bob moved to the platform team
```

**Search:**
```
what do I know about caching decisions?
find: deployment lessons
who is Bob Jones?
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

Four prompt kits to get you started, copy and paste into any AI:

| Kit | Purpose |
|---|---|
| [Kit 1: Memory Migration](prompts/1_memory_migration.md) | Extract what your AI already knows about you and import it |
| [Kit 2: Open Brain Spark](prompts/2_open_brain_spark.md) | Interview to discover your ideal capture workflow |
| [Kit 3: Quick Capture Templates](prompts/3_quick_capture.md) | Sentence starters for fast, structured capture |
| [Kit 4: Weekly Review](prompts/4_weekly_review.md) | End-of-week synthesis with clustering |

---

## Database Schema

The core table, as it stands after all migrations in `sql/`:

```sql
CREATE TYPE thought_type AS ENUM (
    'decision', 'insight', 'person', 'meeting', 'idea', 'note', 'memory'
);

CREATE TABLE thoughts (
    id            UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    content       TEXT          NOT NULL,
    summary       TEXT,
    embedding     vector,                          -- untyped: dimension is config, not schema
    thought_type  thought_type  NOT NULL DEFAULT 'note',
    tags          TEXT[]        NOT NULL DEFAULT '{}',
    source        VARCHAR(100)  NOT NULL DEFAULT 'cli',
    metadata      JSONB         NOT NULL DEFAULT '{}',
    valid_from    TIMESTAMPTZ   DEFAULT NOW(),      -- temporal fact tracking
    valid_until   TIMESTAMPTZ   DEFAULT NULL,
    superseded_by UUID          REFERENCES thoughts(id),
    is_current    BOOLEAN       DEFAULT TRUE,
    created_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);
```

A singleton `embedding_config` table tracks the active model name and dimension (defaults to `nomic-embed-text` / 768) so every entry point can detect a config/DB mismatch at startup. Swapping embedding models is config-only: `ollama pull <new-model>`, update `.env`, then `openbrain reembed`. See `sql/` for the full, authoritative migration history (extensions, indexes, views, hybrid search, temporal facts).

---

## Extending

- **Add a Tailscale tunnel**: see `deploy/caddy-tailscale.conf` (pre-written, just fill in your hostname)
- **Add more thought types**: extend the `thought_type` enum in `sql/002_schema.sql`
- **Replace regex intent parsing**: swap `internal/intent` for an LLM classifier
- **HTTP REST API**: available at `/api/search`, `/api/capture`, `/api/stats`, `/api/review`, and `/api/ingest` (all served by `openbrain-web`)

---

## Privacy

- All data stays on your machine by default
- Embeddings are generated via a local Ollama daemon (`nomic-embed-text`), no API calls
- The one opt-in exception: setting `OPENBRAIN_EXTRACT_PROVIDER=claude` for LLM-based thought extraction sends text to the Anthropic API. Extraction is disabled by default (`none`) if the variable is unset; `.env.example` ships with it pre-filled to `ollama` (local, no cloud call) as the suggested setting
- Telegram and Slack bot tokens never leave your `.env` (bots are not yet implemented, see Quick Start)
- PostgreSQL runs on localhost only (configurable)
- The web UI binds to `127.0.0.1` by default

---

## License

MIT
