-- OpenBrain: Core schema
-- bge-small-en-v1.5 produces 384-dimensional embeddings

CREATE TYPE thought_type AS ENUM (
    'decision',
    'insight',
    'person',
    'meeting',
    'idea',
    'note',
    'memory'
);

CREATE TABLE IF NOT EXISTS thoughts (
    id            UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    content       TEXT          NOT NULL,
    summary       TEXT,                          -- optional short summary for display
    embedding     vector(384),                   -- bge-small-en-v1.5
    thought_type  thought_type  NOT NULL DEFAULT 'note',
    tags          TEXT[]        NOT NULL DEFAULT '{}',
    source        VARCHAR(100)  NOT NULL DEFAULT 'cli',  -- telegram|claude|web|cli|import
    metadata      JSONB         NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ   NOT NULL DEFAULT now()
);

-- Auto-update updated_at on row change
CREATE OR REPLACE FUNCTION touch_updated_at()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

CREATE TRIGGER thoughts_updated_at
    BEFORE UPDATE ON thoughts
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
