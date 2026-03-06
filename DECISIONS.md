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
