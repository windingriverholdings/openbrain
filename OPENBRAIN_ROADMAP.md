# OpenBrain Evolution Roadmap

> From personal second brain to institutional knowledge infrastructure.
> Implement in Claude Code, one phase at a time.

---

## Current State

OpenBrain today: PostgreSQL 17 + pgvector (HNSW cosine, 384-dim bge-small-en-v1.5) + pg_trgm, fastembed ONNX embeddings, regex intent classification, typed thoughts (decision/insight/person/meeting/idea/note/memory), five capture interfaces (Telegram, web, CLI, MCP, file bridge), weekly review, bulk import.

What works well: capture UX, multi-interface access, typed thought taxonomy, self-hosted privacy, MCP integration with Claude Code.

What needs improvement: search quality (vector-only, no keyword fallback), no fact versioning, no relationship graph, no automatic extraction from long-form input, no integration with ArtisanStation.

---

## Phase 1: Hybrid Search (Weekend Project)

**Why first:** Biggest bang for effort. You already have pg_trgm installed. Pure vector search misses exact names, numbers, and terminology. Adding BM25-style full-text search alongside cosine similarity immediately improves retrieval quality for queries like "what did I decide about Redis" where the word "Redis" matters more than semantic similarity.

**What to build:**

### 1.1 Add full-text search index

```sql
-- Add tsvector column to thoughts table
ALTER TABLE thoughts ADD COLUMN fts_vector tsvector
  GENERATED ALWAYS AS (
    setweight(to_tsvector('english', coalesce(summary, '')), 'A') ||
    setweight(to_tsvector('english', content), 'B')
  ) STORED;

CREATE INDEX idx_thoughts_fts ON thoughts USING GIN (fts_vector);
```

Summary gets weight A (higher priority), content gets weight B. This means a keyword match in your summary ranks higher than the same word buried in a long thought.

### 1.2 Hybrid search function

```sql
CREATE OR REPLACE FUNCTION hybrid_search(
  query_text TEXT,
  query_embedding vector(384),
  match_count INT DEFAULT 10,
  keyword_weight FLOAT DEFAULT 0.3,
  semantic_weight FLOAT DEFAULT 0.7
)
RETURNS TABLE (
  id UUID,
  content TEXT,
  summary TEXT,
  thought_type thought_type,
  tags TEXT[],
  source VARCHAR(64),
  created_at TIMESTAMPTZ,
  keyword_score FLOAT,
  semantic_score FLOAT,
  combined_score FLOAT
) AS $$
  WITH keyword_results AS (
    SELECT
      t.id,
      ts_rank_cd(t.fts_vector, plainto_tsquery('english', query_text)) AS score
    FROM thoughts t
    WHERE t.fts_vector @@ plainto_tsquery('english', query_text)
    ORDER BY score DESC
    LIMIT match_count * 3
  ),
  semantic_results AS (
    SELECT
      t.id,
      1 - (t.embedding <=> query_embedding) AS score
    FROM thoughts t
    ORDER BY t.embedding <=> query_embedding
    LIMIT match_count * 3
  ),
  combined AS (
    SELECT
      COALESCE(k.id, s.id) AS id,
      COALESCE(k.score, 0) AS kw_score,
      COALESCE(s.score, 0) AS sem_score
    FROM keyword_results k
    FULL OUTER JOIN semantic_results s ON k.id = s.id
  )
  SELECT
    t.id,
    t.content,
    t.summary,
    t.thought_type,
    t.tags,
    t.source,
    t.created_at,
    c.kw_score AS keyword_score,
    c.sem_score AS semantic_score,
    (c.kw_score * keyword_weight + c.sem_score * semantic_weight) AS combined_score
  FROM combined c
  JOIN thoughts t ON t.id = c.id
  ORDER BY (c.kw_score * keyword_weight + c.sem_score * semantic_weight) DESC
  LIMIT match_count;
$$ LANGUAGE sql STABLE;
```

### 1.3 Update brain.py search

Update the search dispatcher to call hybrid_search instead of pure cosine similarity. Keep the existing vector-only search as a fallback option (flag `--mode vector|keyword|hybrid`, default hybrid).

### 1.4 Update MCP tool

Update `search_thoughts` MCP tool to use hybrid search by default. Add optional `mode` parameter for callers that want keyword-only or vector-only.

**Validation:** Search for "Redis caching decision" — hybrid should surface the exact thought with "Redis" in it at rank 1, where pure vector might rank it lower behind semantically similar but less precise matches.

---

## Phase 2: Temporal Fact Tracking (1-2 Day Project)

**Why second:** Your person and decision thought types naturally evolve over time. "Sarah wants corner booth" becomes "Sarah moved to booth 7" becomes "Sarah left Alley Kat." Without temporal tracking, you get contradictory thoughts cluttering search results.

**What to build:**

### 2.1 Schema additions

```sql
-- Fact validity tracking
ALTER TABLE thoughts ADD COLUMN valid_from TIMESTAMPTZ DEFAULT NOW();
ALTER TABLE thoughts ADD COLUMN valid_until TIMESTAMPTZ DEFAULT NULL;
ALTER TABLE thoughts ADD COLUMN superseded_by UUID REFERENCES thoughts(id);
ALTER TABLE thoughts ADD COLUMN is_current BOOLEAN DEFAULT TRUE;

-- Index for fast "current facts" queries
CREATE INDEX idx_thoughts_current ON thoughts (is_current) WHERE is_current = TRUE;

-- Entity linking table for connecting thoughts about the same subject
CREATE TABLE thought_subjects (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  thought_id UUID NOT NULL REFERENCES thoughts(id) ON DELETE CASCADE,
  subject_name TEXT NOT NULL,        -- normalized name: "sarah chen", "redis", "booth 7"
  subject_type VARCHAR(32),          -- person, tool, location, vendor, instructor, etc.
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_thought_subjects_name ON thought_subjects (lower(subject_name));
CREATE INDEX idx_thought_subjects_thought ON thought_subjects (thought_id);
```

### 2.2 Supersede function

```sql
CREATE OR REPLACE FUNCTION supersede_thought(
  old_thought_id UUID,
  new_thought_id UUID
) RETURNS VOID AS $$
BEGIN
  UPDATE thoughts SET
    is_current = FALSE,
    valid_until = NOW(),
    superseded_by = new_thought_id
  WHERE id = old_thought_id;
END;
$$ LANGUAGE plpgsql;
```

### 2.3 Intent classification update

Add supersede intent to the regex classifier in intent.py:

```
"actually, Sarah moved to booth 7"     -> capture + supersede search for "Sarah" + "booth"
"update: we switched from Redis to Valkey" -> capture + supersede search for "Redis"
"correction: the rate limit is 2000, not 1000" -> capture + supersede matching thought
```

Pattern: Words like "actually," "update:", "correction:", "changed:", "no longer", "now instead" signal a supersede operation. Search for the most relevant existing current thought on the same subject and mark it superseded.

### 2.4 Search filter

Default search behavior: only return `is_current = TRUE` thoughts. Add `--include-history` flag to show the full timeline for a subject.

**New MCP tool: `thought_timeline`** — given a subject name, return all thoughts (current and superseded) in chronological order. This gives your agents a full history of how knowledge about a subject evolved.

**Validation:** Capture "decided to use Redis for caching." Then capture "actually, switched from Redis to Valkey for caching." The original Redis thought should be marked superseded. Searching "caching decision" should return only the Valkey thought. `thought_timeline "caching"` should show both in order.

---

## Phase 3: LLM-Assisted Extraction (2-3 Day Project)

**Why third:** Once you have hybrid search and temporal tracking, the bottleneck becomes capture quality. Long meeting notes, voice memos, or pasted conversations contain multiple facts, decisions, and people references. Manual capture of each one is tedious. LLM extraction turns a single messy input into multiple structured thoughts.

**What to build:**

### 3.1 Extraction pipeline

New module: `extract.py`

Input: raw text (meeting notes, pasted conversation, long-form capture).
Output: list of structured thought candidates.

```python
EXTRACTION_PROMPT = """
Analyze this text and extract distinct thoughts. For each thought, provide:
- content: the core information (1-3 sentences, standalone)
- thought_type: one of decision, insight, person, meeting, idea, note, memory
- tags: relevant tags as a list
- subjects: people, tools, places, or concepts this thought is about
- supersedes_query: if this updates a previous fact, a search query to find the old thought (null otherwise)

Text to analyze:
{input_text}

Return JSON array only, no other text.
"""
```

### 3.2 LLM provider abstraction

Keep it simple — support two backends:

1. **Local (Ollama):** For privacy-sensitive extraction. Use a small model like phi3 or llama3.2. Slower but no API costs, no data leaving machine.
2. **Claude API:** For higher quality extraction when you don't mind the API call. Use Haiku for cost efficiency.

Config in `.env`:
```
OPENBRAIN_EXTRACT_PROVIDER=ollama    # or "claude"
OPENBRAIN_EXTRACT_MODEL=phi3:mini    # or "claude-haiku-4-5-20251001"
OPENBRAIN_ANTHROPIC_API_KEY=...      # only needed for claude provider
```

### 3.3 Capture flow update

Two modes:

- **Quick capture (existing):** Short statement → regex classifies → single thought stored. No LLM needed. Keep this fast path.
- **Deep capture (new):** Long text or explicit `extract:` prefix → LLM extraction → multiple thought candidates → user confirms or auto-accepts.

In Telegram/web UI, if input is longer than ~200 characters or starts with "extract:", route to deep capture. Show extracted thoughts as a preview: "I found 4 thoughts in your input: [list]. Save all? Or edit?"

### 3.4 MCP tool

New tool: `extract_thoughts` — takes raw text, returns structured candidates. The agent can then call `capture_thought` for each one, optionally with supersede logic.

This is powerful for Claude Code workflows: paste in meeting notes, agent extracts and stores everything, including superseding outdated facts automatically.

**Validation:** Paste in a paragraph of meeting notes mentioning three people, two decisions, and an idea. Extraction should produce 6 separate typed thoughts with correct classifications and subject links.

---

## Phase 4: Knowledge Graph Visualization (3-5 Day Project)

**Why fourth:** This is the "wow factor" feature but requires the subject linking from Phase 2 and works best with the enriched data from Phase 3. It turns OpenBrain from a searchable database into a navigable knowledge map.

**What to build:**

### 4.1 Graph query API

New endpoints in the FastAPI web server:

```
GET /api/graph/subjects
  → list all subjects with thought counts and types

GET /api/graph/subject/{name}
  → all thoughts linked to a subject, with connections to other subjects

GET /api/graph/connections
  → subject-to-subject connections (two subjects linked by shared thoughts)
  → optional: filter by thought_type, date range, tags

GET /api/graph/neighborhood/{name}?depth=2
  → ego graph: a subject and everything within N hops
```

### 4.2 Graph data structure

```python
# Graph node: a subject (person, tool, concept, location)
{
  "id": "sarah-chen",
  "label": "Sarah Chen",
  "type": "person",
  "thought_count": 12,
  "latest_thought": "2026-03-10T...",
  "is_current": true
}

# Graph edge: two subjects co-occurring in the same thought
{
  "source": "sarah-chen",
  "target": "booth-7",
  "weight": 3,              # number of shared thoughts
  "thought_types": ["decision", "note"],
  "latest": "2026-03-10T..."
}
```

### 4.3 Web UI visualization

Add a `/graph` route to the existing web UI. Use D3.js force-directed graph (no extra dependencies beyond what a CDN provides).

Features:
- Nodes sized by thought count
- Nodes colored by subject type (people = blue, tools = green, locations = orange, etc.)
- Edges weighted by co-occurrence count
- Click a node to see its thoughts in a sidebar
- Filter by thought type, date range, tags
- Search to center on a specific subject

Keep it simple — this is a tool for you and Jeanette, not a product feature (yet).

### 4.4 Automatic subject extraction

When a thought is captured (either quick or deep capture), automatically extract subject references. For quick capture without LLM:

- Person type thoughts: the subject is the person mentioned
- Decision type thoughts: extract tool/technology names via simple NER patterns
- Tag-based: every tag becomes a potential subject link

For deep capture (Phase 3): the LLM already extracts subjects, so just store the links.

**Validation:** After a few weeks of use, navigate to the graph view. You should see clusters — ArtisanStation-related subjects grouped together, thePUNShop subjects in another cluster, personal decisions in another. Click on "Sarah Chen" and see every thought you've ever captured about her, with connections to booths, classes, and decisions she's involved in.

---

## Phase 5: ArtisanStation Integration (Ongoing)

**Why last:** This builds on everything above and is the long-term payoff — turning OpenBrain from a personal tool into institutional infrastructure for your business.

**What to build:**

### 5.1 ArtisanStation-scoped MCP tools

New MCP tools specifically for ArtisanStation agent workflows:

```
vendor_context(vendor_name)
  → searches OpenBrain for all current thoughts about this vendor
  → returns: preferences, history, notes, related people, booth history

instructor_context(instructor_name)
  → same for instructors: reliability notes, student feedback, scheduling patterns

student_context(student_name)
  → enrollment history, preferences, completion rates, notes

location_context(booth_or_room)
  → what you know about this physical space: who's been there, what works, issues
```

These are thin wrappers around hybrid_search + thought_timeline, scoped by tags or subjects.

### 5.2 Capture hooks from ArtisanStation

When things happen in ArtisanStation, automatically capture thoughts:

```python
# After a class is completed
capture_thought(
  content=f"{instructor} taught {class_name} on {date}. {attendance} students attended out of {capacity} capacity.",
  thought_type="meeting",
  tags=["artisanstation", "class", instructor_slug],
  source="artisanstation-auto"
)

# After a booth assignment change
capture_thought(
  content=f"{vendor} moved from {old_booth} to {new_booth}.",
  thought_type="note",
  tags=["artisanstation", "booth", vendor_slug],
  source="artisanstation-auto",
  supersedes_query=f"{vendor} booth assignment"
)

# After a waiver is signed (DocuSeal integration)
capture_thought(
  content=f"{student} signed waiver for {class_name}.",
  thought_type="note",
  tags=["artisanstation", "waiver", student_slug],
  source="docuseal-auto"
)
```

### 5.3 Context panel in ArtisanStation UI

Long-term goal: when viewing a vendor profile, instructor profile, or booking screen in ArtisanStation, show a "What we know" panel that pulls from OpenBrain.

This could be as simple as an HTMX partial that calls `/api/search?q={entity_name}&tags=artisanstation&limit=5` and renders the results inline.

### 5.4 Jeanette's workflow

This is the real test. Jeanette is at the shop, a vendor asks about changing booths. She:

1. Opens Telegram, messages OpenBrain: "what do I know about Mike's booth situation?"
2. Gets back: Mike's current booth assignment, any previous notes about his preferences, relevant decisions about booth layout.
3. Makes the decision, messages: "moved Mike from booth 3 to booth 5, he wanted more foot traffic near the entrance"
4. OpenBrain captures this, supersedes the old booth assignment, links to Mike and both booth locations.
5. Next time anyone (human or agent) asks about Mike or booth assignments, this context is there.

---

## Implementation Order Summary

| Phase | Effort | Dependencies | Impact |
|-------|--------|-------------|--------|
| 1. Hybrid Search | Weekend | None (pg_trgm already installed) | High — immediate search quality improvement |
| 2. Temporal Facts | 1-2 days | None (schema additions only) | High — solves contradictory thoughts problem |
| 3. LLM Extraction | 2-3 days | Phase 2 (for supersede logic) | Medium — quality of life for heavy capture |
| 4. Graph Visualization | 3-5 days | Phase 2 (for subject linking) | Medium — insight and navigation |
| 5. ArtisanStation Integration | Ongoing | Phases 1-3 minimum | High — turns OpenBrain into business infrastructure |

---

## Technical Notes

- **Embedding model:** Stay with bge-small-en-v1.5 for now. It's fast, small, and good enough. If you add multilingual needs later (Japanese garment descriptions?), swap to Qwen3-Embedding like QMD does.
- **pg_trgm vs FTS5:** You're using Postgres, not SQLite, so you get the real tsvector full-text search plus pg_trgm for fuzzy trigram matching. Use tsvector for the BM25-style ranking and pg_trgm as a fallback for misspellings.
- **No Ollama dependency for Phases 1-2:** Hybrid search and temporal facts are pure SQL + Python. No new infrastructure needed.
- **Backward compatible:** All schema changes are additive. Existing thoughts get `is_current = TRUE` and `valid_from = created_at` by default. Nothing breaks.
- **Test with real data:** You already have thoughts in the database. Run Phase 1's hybrid search against your existing corpus before and after to measure improvement.
