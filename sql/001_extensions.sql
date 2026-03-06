-- OpenBrain: Enable required PostgreSQL extensions
-- Run as superuser or a role with CREATE EXTENSION privilege

CREATE EXTENSION IF NOT EXISTS vector;        -- pgvector: semantic search
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";   -- gen_random_uuid() fallback
CREATE EXTENSION IF NOT EXISTS pg_trgm;       -- trigram: fuzzy full-text search
