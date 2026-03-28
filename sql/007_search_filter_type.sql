-- OpenBrain: OB-001 — Add filter_type parameter to hybrid_search()
-- Allows filtering search results by thought_type at the SQL level,
-- applied in both keyword_results and semantic_results CTEs.

CREATE OR REPLACE FUNCTION hybrid_search(
  query_text TEXT,
  query_embedding vector(384),
  match_count INT DEFAULT 10,
  keyword_weight FLOAT DEFAULT 0.3,
  semantic_weight FLOAT DEFAULT 0.7,
  min_score FLOAT DEFAULT 0.01,
  current_only BOOLEAN DEFAULT TRUE,
  filter_type TEXT DEFAULT NULL
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
      AND (filter_type IS NULL OR t.thought_type = filter_type::thought_type)
    ORDER BY score DESC
    LIMIT match_count * 3
  ),
  semantic_results AS (
    SELECT
      t.id,
      1 - (t.embedding <=> query_embedding) AS score
    FROM thoughts t
    WHERE (NOT current_only OR t.is_current = TRUE)
      AND (filter_type IS NULL OR t.thought_type = filter_type::thought_type)
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
