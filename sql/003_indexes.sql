-- OpenBrain: Indexes for performance

-- HNSW vector index for approximate nearest-neighbour semantic search
-- m=16, ef_construction=64 are good defaults for a personal-scale dataset
CREATE INDEX IF NOT EXISTS thoughts_embedding_hnsw
    ON thoughts USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- Thought type filtering
CREATE INDEX IF NOT EXISTS thoughts_type_idx
    ON thoughts (thought_type);

-- Time-range queries (weekly review, recency ranking)
CREATE INDEX IF NOT EXISTS thoughts_created_at_idx
    ON thoughts (created_at DESC);

-- Tag array lookups (GIN for ANY/ALL/overlap operators)
CREATE INDEX IF NOT EXISTS thoughts_tags_gin
    ON thoughts USING gin (tags);

-- JSONB metadata queries
CREATE INDEX IF NOT EXISTS thoughts_metadata_gin
    ON thoughts USING gin (metadata jsonb_path_ops);

-- Trigram index for fuzzy full-text search on content
CREATE INDEX IF NOT EXISTS thoughts_content_trgm
    ON thoughts USING gin (content gin_trgm_ops);

-- Source filtering (which interface captured this thought)
CREATE INDEX IF NOT EXISTS thoughts_source_idx
    ON thoughts (source);
