# OpenBrain — Architectural Decisions

> All key decisions made during the design and build of this system, with rationale.

---

## Decision Log

### 001 — Database: PostgreSQL + pgvector
**Date:** 2026-03-05
**Decision:** Use PostgreSQL 16 with the pgvector extension as the primary data store.
**Rationale:** Already installed on the host system. PostgreSQL is battle-tested, supports full-text search natively, and pgvector adds high-performance approximate nearest neighbour (ANN) search via HNSW and IVFFlat indexes. TimescaleDB is also present, which enables efficient time-series queries on thought history without extra infrastructure.
**Alternatives considered:** SQLite + sqlite-vec (too limited for multi-interface access), Qdrant (extra service to manage), ChromaDB (less mature SQL integration).

---

### 002 — Embedding Model: fastembed + BAAI/bge-small-en-v1.5
**Date:** 2026-03-05
**Decision:** Use `fastembed` (Qdrant's ONNX-based Python library) with the `BAAI/bge-small-en-v1.5` model.
**Rationale:**
- Fully local — no cloud API, no external service dependency.
- Runs in-process (no HTTP overhead, unlike Ollama).
- ONNX Runtime is highly optimized for CPU inference.
- `bge-small-en-v1.5` is 384-dimensional, ~130MB on disk, and consistently ranks top-tier on the MTEB benchmark for its size class.
- Significantly faster on CPU-only hardware than larger models (e.g. nomic-embed-text at 768 dims).
**Alternatives considered:** Ollama + nomic-embed-text (requires Ollama daemon running, more overhead), OpenAI embeddings (cloud dependency, violates privacy requirement), sentence-transformers (fastembed is a faster drop-in via ONNX).

---

### 003 — MCP Server Language: Python
**Date:** 2026-03-05
**Decision:** Implement the MCP server in Python using the official `mcp` SDK.
**Rationale:** fastembed and asyncpg are Python-native. The official Anthropic MCP SDK has first-class Python support. Keeping the stack in one language reduces complexity.

---

### 004 — Package Manager: pixi
**Date:** 2026-03-05
**Decision:** Use `pixi` for environment and dependency management.
**Rationale:** User's stated preference across all projects. Provides reproducible environments with a single `pixi.toml` lockfile.

---

### 005 — Vector Index: HNSW (cosine similarity)
**Date:** 2026-03-05
**Decision:** Use HNSW index with cosine distance for the embedding column.
**Rationale:** HNSW (Hierarchical Navigable Small World) provides faster query performance than IVFFlat and does not require a training step. Cosine similarity is standard for sentence embeddings from bge-small.

---

### 006 — Thought Schema: typed with JSONB metadata
**Date:** 2026-03-05
**Decision:** Each thought has a `thought_type` enum (decision, insight, person, meeting, idea, note), a `tags` array, and a free-form `metadata JSONB` column.
**Rationale:** The four prompt kits (Memory Migration, Open Brain Spark, Quick Capture, Weekly Review) all produce structured but variable metadata. A typed column covers the common query patterns; JSONB covers the long tail without requiring schema migrations per thought type.

---

### 007 — Source Tracking
**Date:** 2026-03-05
**Decision:** Every thought stores a `source` field (e.g. `telegram`, `claude`, `web`, `cli`, `import`).
**Rationale:** The system is designed to ingest from any interface. Knowing provenance enables filtering, auditing, and interface-specific workflows.

---

## Open Questions / Future Decisions

- [ ] Authentication strategy for multi-device MCP access (currently single-user, token-based)
- [ ] Chunking strategy for long-form thoughts (> 512 tokens)
- [ ] Scheduled weekly review automation (cron vs. event-driven)
- [ ] Export format for portability (JSON-L, Markdown, Obsidian vault)

---

### 008 — Work Branch Interfaces: Telegram + Web UI (no OpenClaw)
**Date:** 2026-03-05
**Decision:** Work branch uses a direct Telegram bot and a local web chat UI. No OpenClaw dependency.
**Rationale:** OpenClaw is not yet trusted/comfortable for the work context. Telegram is more secure than Slack (bot token stays local, no third-party workspace). Web UI provides a desktop capture interface without requiring CLI knowledge.

---

### 009 — Web UI: FastAPI + WebSockets, local-only on port 10203
**Date:** 2026-03-05
**Decision:** FastAPI with WebSocket transport, bound to 127.0.0.1:10203, accessible via `mybrain.local` hostname.
**Rationale:** WebSockets give real-time feel (typing indicator, instant response). Local-only eliminates auth complexity for now. `mybrain.local` via /etc/hosts is friendlier than remembering a port. FastAPI is async-native, matching the rest of the stack.
**Alternatives considered:** SSE (simpler but one-directional), plain HTTP polling (laggy).

---

### 010 — Intent Parsing: Regex-based, no LLM required
**Date:** 2026-03-05
**Decision:** Natural language input is parsed by a lightweight regex classifier (intent.py), not an LLM.
**Rationale:** Keeps the web UI and Telegram bot self-contained with zero additional model overhead. Common patterns (search/capture/review) are reliably detected. Fallback is to capture anything statement-like and search anything question-like.
**TODO:** Replace with a local LLM classifier (Ollama + llama3) for richer NL understanding when needed.

---

### 011 — PostgreSQL: Bare metal on host (not Docker)
**Date:** 2026-03-05
**Decision:** PostgreSQL runs as a native system service (already installed). Web UI can optionally run in Docker with host network bindings to reach it.
**Rationale:** PostgreSQL is already installed and running on the host. Keeping it native avoids Docker networking complexity for the database. The web server is stateless and safe to containerise.

---

### 014 — Systemd: User Services Over System Services
**Date:** 2026-03-05
**Decision:** All three daemons (openbrain-web, openbrain-telegram, openbrain-watchd) run as `systemd --user` services installed to `~/.config/systemd/user/`, not as system-level services in `/etc/systemd/system/`.
**Rationale:**
- **Least privilege:** Services run as the owning user, never as root. They cannot write to system directories or read other users' files.
- **No sudo for daily operations:** `systemctl --user start/stop/restart/status` requires no elevated privileges.
- **Credential isolation:** `.env` (containing DB password and bot token) is only readable by the user — a system service running as a different account couldn't access it.
- **Simpler unit files:** No `User=%i` directive or template `@.service` pattern needed. `%h` resolves to the user's home directory automatically.
- **Consistent with watchd:** The file bridge daemon was already a user service; this makes all three consistent.
**Trade-off:** Services need `loginctl enable-linger <user>` to start at boot without an active login session. `install-services.sh` handles this automatically (requires sudo once).
**The only steps requiring sudo:** Adding `mybrain.local` to `/etc/hosts` and running `loginctl enable-linger` — both one-time setup operations.

---

### 013 — OpenClaw Integration: File Bridge Over Binary/Network Bridge
**Date:** 2026-03-05
**Decision:** OpenBrain is exposed to the OpenClaw agent via a file-based RPC bridge rather than a binary CLI or HTTP call.
**Rationale:** OpenClaw runs the agent inside an isolated sandbox container (`~/.openclaw/sandboxes/agent-main-*/`). The sandbox has no access to host binaries (no mcporter, curl, wget, python) and no guaranteed network path to localhost. The only reliable capability is file read/write to `/workspace` (which maps to the sandbox directory on the host). The file bridge exploits this: the agent writes a JSON request file, a host-side daemon (`openbrain-watchd`) picks it up, calls OpenBrain directly, and writes back a JSON response file. This requires zero binaries and zero network access from inside the sandbox.
**Security properties gained:**
- The agent cannot directly invoke host processes or binaries
- The agent cannot make arbitrary network calls
- OpenBrain credentials (DB password) never enter the sandbox — only the watchd daemon holds them
- The attack surface from a compromised agent prompt is limited to what can be expressed in a JSON request file
**Alternatives considered:** mcporter CLI (not available in sandbox PATH), curl/wget HTTP calls (neither available), Python urllib (no Python in sandbox), spawn sub-agent (adds orchestration complexity and latency).

---

### 012 — Tailscale: Deferred, TODOs placed throughout codebase
**Date:** 2026-03-05
**Decision:** Local-only for now. All Tailscale expansion points are marked with `# TODO(tailscale):` comments.
**Rationale:** Fastest path to a working system. Tailscale + Caddy integration is one config change away when ready.
**TODO locations:** web/app.py, telegram_bot.py, install-services.sh, deploy/caddy-tailscale.conf

