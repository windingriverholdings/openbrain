-- 009_embedding_config.sql
-- Singleton table tracking the active embedding model and dimension.
-- Used by all entry points to detect config/DB mismatch at startup.

CREATE TABLE IF NOT EXISTS embedding_config (
  id         BOOLEAN      PRIMARY KEY DEFAULT TRUE CHECK (id),
  model_name TEXT         NOT NULL,
  dimension  INT          NOT NULL CHECK (dimension > 0),
  updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

INSERT INTO embedding_config (model_name, dimension)
VALUES ('nomic-embed-text', 768)
ON CONFLICT (id) DO NOTHING;
