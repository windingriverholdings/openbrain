-- OpenBrain: Phase 2 — Temporal Fact Tracking
-- Adds validity tracking, supersede chain, and subject linking.

-- 1. Fact validity columns on thoughts
ALTER TABLE thoughts ADD COLUMN IF NOT EXISTS valid_from TIMESTAMPTZ DEFAULT NOW();
ALTER TABLE thoughts ADD COLUMN IF NOT EXISTS valid_until TIMESTAMPTZ DEFAULT NULL;
ALTER TABLE thoughts ADD COLUMN IF NOT EXISTS superseded_by UUID REFERENCES thoughts(id);
ALTER TABLE thoughts ADD COLUMN IF NOT EXISTS is_current BOOLEAN DEFAULT TRUE;

-- 2. Backfill existing rows: valid_from = created_at, is_current = true
UPDATE thoughts SET valid_from = created_at WHERE valid_from IS NULL;
UPDATE thoughts SET is_current = TRUE WHERE is_current IS NULL;

-- 3. Partial index for fast "current facts" queries
CREATE INDEX IF NOT EXISTS idx_thoughts_current
  ON thoughts (is_current) WHERE is_current = TRUE;

-- 4. Subject linking table — connects thoughts to entities
CREATE TABLE IF NOT EXISTS thought_subjects (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  thought_id UUID NOT NULL REFERENCES thoughts(id) ON DELETE CASCADE,
  subject_name TEXT NOT NULL,
  subject_type VARCHAR(32),
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_thought_subjects_name
  ON thought_subjects (lower(subject_name));
CREATE INDEX IF NOT EXISTS idx_thought_subjects_thought
  ON thought_subjects (thought_id);

-- 5. Supersede function: mark an old thought as superseded by a new one
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

-- 6. Update the hybrid_search function to respect is_current filter
CREATE OR REPLACE FUNCTION hybrid_search(
  query_text TEXT,
  query_embedding vector(384),
  match_count INT DEFAULT 10,
  keyword_weight FLOAT DEFAULT 0.3,
  semantic_weight FLOAT DEFAULT 0.7,
  min_score FLOAT DEFAULT 0.01,
  current_only BOOLEAN DEFAULT TRUE
)
RETURNS TABLE (
  id UUID,
  content TEXT,
  summary TEXT,
  thought_type thought_type,
  tags TEXT[],
  source VARCHAR(100),
  metadata JSONB,
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
      AND (NOT current_only OR t.is_current = TRUE)
    ORDER BY score DESC
    LIMIT match_count * 3
  ),
  semantic_results AS (
    SELECT
      t.id,
      1 - (t.embedding <=> query_embedding) AS score
    FROM thoughts t
    WHERE (NOT current_only OR t.is_current = TRUE)
    ORDER BY t.embedding <=> query_embedding
    LIMIT match_count * 3
  ),
  combined AS (
    SELECT
      COALESCE(k.id, s.id) AS id,
      COALESCE(k.score, 0)::FLOAT AS kw_score,
      COALESCE(s.score, 0)::FLOAT AS sem_score
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
    t.metadata,
    t.created_at,
    c.kw_score AS keyword_score,
    c.sem_score AS semantic_score,
    (c.kw_score * keyword_weight + c.sem_score * semantic_weight)::FLOAT AS combined_score
  FROM combined c
  JOIN thoughts t ON t.id = c.id
  WHERE (c.kw_score * keyword_weight + c.sem_score * semantic_weight) >= min_score
  ORDER BY (c.kw_score * keyword_weight + c.sem_score * semantic_weight) DESC
  LIMIT match_count;
$$ LANGUAGE sql STABLE;

-- 7. Update views to respect is_current
CREATE OR REPLACE VIEW recent_thoughts AS
SELECT
    id, content, summary, thought_type, tags, source, metadata, created_at
FROM thoughts
WHERE is_current = TRUE
ORDER BY created_at DESC;

CREATE OR REPLACE VIEW this_week_thoughts AS
SELECT
    id, content, summary, thought_type, tags, source, metadata, created_at
FROM thoughts
WHERE created_at >= now() - INTERVAL '7 days'
  AND is_current = TRUE
ORDER BY created_at DESC;
