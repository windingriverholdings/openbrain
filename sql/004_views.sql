-- OpenBrain: Convenience views

-- Recent thoughts without embeddings (lighter for display)
CREATE OR REPLACE VIEW recent_thoughts AS
SELECT
    id,
    content,
    summary,
    thought_type,
    tags,
    source,
    metadata,
    created_at
FROM thoughts
ORDER BY created_at DESC;

-- Weekly view: thoughts from the last 7 days
CREATE OR REPLACE VIEW this_week_thoughts AS
SELECT
    id,
    content,
    summary,
    thought_type,
    tags,
    source,
    metadata,
    created_at
FROM thoughts
WHERE created_at >= now() - INTERVAL '7 days'
ORDER BY created_at DESC;

-- Tag frequency summary
CREATE OR REPLACE VIEW tag_summary AS
SELECT
    tag,
    count(*) AS thought_count,
    max(created_at) AS last_used
FROM thoughts, unnest(tags) AS tag
GROUP BY tag
ORDER BY thought_count DESC;

-- Thought type breakdown
CREATE OR REPLACE VIEW thought_type_summary AS
SELECT
    thought_type,
    count(*) AS count,
    max(created_at) AS latest
FROM thoughts
GROUP BY thought_type
ORDER BY count DESC;
