"""PostgreSQL connection pool and core data-access functions."""

from __future__ import annotations

from typing import Any

import asyncpg
import structlog

from .config import get_config

logger = structlog.get_logger(__name__)

_pool: asyncpg.Pool | None = None


async def get_pool() -> asyncpg.Pool:
    global _pool
    if _pool is None:
        config = get_config()
        logger.info("connecting_to_db", host=config.db_host, db=config.db_name)
        _pool = await asyncpg.create_pool(
            host=config.db_host,
            port=config.db_port,
            database=config.db_name,
            user=config.db_user,
            password=config.db_password,
            min_size=1,
            max_size=5,
            command_timeout=30,
        )
    return _pool


async def close_pool() -> None:
    global _pool
    if _pool is not None:
        await _pool.close()
        _pool = None


def _vec(embedding: list[float]) -> str:
    """Encode a float list as a pgvector literal: [0.1,0.2,...]"""
    return "[" + ",".join(str(x) for x in embedding) + "]"


# ── Thoughts ─────────────────────────────────────────────────────────────────

async def insert_thought(
    *,
    content: str,
    embedding: list[float],
    thought_type: str = "note",
    tags: list[str] | None = None,
    source: str = "cli",
    summary: str | None = None,
    metadata: dict[str, Any] | None = None,
) -> str:
    """Insert a thought and return its UUID."""
    import json

    pool = await get_pool()
    row = await pool.fetchrow(
        """
        INSERT INTO thoughts (content, embedding, thought_type, tags, source, summary, metadata)
        VALUES ($1, $2::vector, $3::thought_type, $4, $5, $6, $7)
        RETURNING id
        """,
        content,
        _vec(embedding),
        thought_type,
        tags or [],
        source,
        summary,
        json.dumps(metadata or {}),
    )
    thought_id = str(row["id"])
    logger.info("thought_inserted", id=thought_id, type=thought_type, source=source)
    return thought_id


async def search_thoughts(
    *,
    embedding: list[float],
    top_k: int = 10,
    thought_type: str | None = None,
    tags: list[str] | None = None,
    score_threshold: float = 0.35,
) -> list[dict[str, Any]]:
    """Semantic search over thoughts using cosine similarity."""
    pool = await get_pool()

    filters = []
    args: list[Any] = [_vec(embedding), top_k]
    arg_idx = 3

    if thought_type:
        filters.append(f"thought_type = ${arg_idx}::thought_type")
        args.append(thought_type)
        arg_idx += 1

    if tags:
        filters.append(f"tags && ${arg_idx}")
        args.append(tags)
        arg_idx += 1

    where_clause = ("WHERE " + " AND ".join(filters)) if filters else ""

    rows = await pool.fetch(
        f"""
        SELECT
            id,
            content,
            summary,
            thought_type,
            tags,
            source,
            metadata,
            created_at,
            1 - (embedding <=> $1::vector) AS score
        FROM thoughts
        {where_clause}
        ORDER BY embedding <=> $1::vector
        LIMIT $2
        """,
        *args,
    )

    return [
        {
            "id": str(r["id"]),
            "content": r["content"],
            "summary": r["summary"],
            "thought_type": r["thought_type"],
            "tags": list(r["tags"]),
            "source": r["source"],
            "metadata": r["metadata"],
            "created_at": r["created_at"].isoformat(),
            "score": round(float(r["score"]), 4),
        }
        for r in rows
        if float(r["score"]) >= score_threshold
    ]


async def hybrid_search_thoughts(
    *,
    query_text: str,
    embedding: list[float],
    top_k: int = 10,
    keyword_weight: float = 0.3,
    semantic_weight: float = 0.7,
    score_threshold: float = 0.01,
    include_history: bool = False,
) -> list[dict[str, Any]]:
    """Hybrid search combining keyword (BM25) and semantic (cosine) scoring."""
    pool = await get_pool()
    rows = await pool.fetch(
        """
        SELECT * FROM hybrid_search($1, $2::vector, $3, $4, $5, $6, $7)
        """,
        query_text,
        _vec(embedding),
        top_k,
        keyword_weight,
        semantic_weight,
        score_threshold,
        not include_history,
    )
    return [
        {
            "id": str(r["id"]),
            "content": r["content"],
            "summary": r["summary"],
            "thought_type": r["thought_type"],
            "tags": list(r["tags"]),
            "source": r["source"],
            "metadata": r["metadata"],
            "created_at": r["created_at"].isoformat(),
            "keyword_score": round(float(r["keyword_score"]), 4),
            "semantic_score": round(float(r["semantic_score"]), 4),
            "score": round(float(r["combined_score"]), 4),
        }
        for r in rows
    ]


async def keyword_search_thoughts(
    *,
    query_text: str,
    top_k: int = 10,
    include_history: bool = False,
) -> list[dict[str, Any]]:
    """Full-text keyword search using tsvector ranking."""
    pool = await get_pool()
    current_filter = "" if include_history else " AND is_current = TRUE"
    rows = await pool.fetch(
        f"""
        SELECT
            id, content, summary, thought_type, tags, source, metadata, created_at,
            ts_rank_cd(fts_vector, plainto_tsquery('english', $1)) AS score
        FROM thoughts
        WHERE fts_vector @@ plainto_tsquery('english', $1){current_filter}
        ORDER BY score DESC
        LIMIT $2
        """,
        query_text,
        top_k,
    )
    return [
        {
            "id": str(r["id"]),
            "content": r["content"],
            "summary": r["summary"],
            "thought_type": r["thought_type"],
            "tags": list(r["tags"]),
            "source": r["source"],
            "metadata": r["metadata"],
            "created_at": r["created_at"].isoformat(),
            "score": round(float(r["score"]), 4),
        }
        for r in rows
    ]


async def get_thoughts_since(days: int = 7) -> list[dict[str, Any]]:
    """Fetch all thoughts from the last N days (for weekly review)."""
    pool = await get_pool()
    rows = await pool.fetch(
        """
        SELECT id, content, summary, thought_type, tags, source, metadata, created_at
        FROM thoughts
        WHERE created_at >= now() - ($1 || ' days')::INTERVAL
        ORDER BY created_at DESC
        """,
        str(days),
    )
    return [
        {
            "id": str(r["id"]),
            "content": r["content"],
            "summary": r["summary"],
            "thought_type": r["thought_type"],
            "tags": list(r["tags"]),
            "source": r["source"],
            "metadata": r["metadata"],
            "created_at": r["created_at"].isoformat(),
        }
        for r in rows
    ]


async def get_stats() -> dict[str, Any]:
    """Return aggregate stats about the thought database."""
    pool = await get_pool()
    row = await pool.fetchrow(
        """
        SELECT
            count(*) AS total,
            count(*) FILTER (WHERE created_at >= now() - INTERVAL '7 days') AS this_week,
            count(*) FILTER (WHERE created_at >= now() - INTERVAL '1 day') AS today,
            min(created_at) AS oldest,
            max(created_at) AS newest
        FROM thoughts
        """
    )
    type_rows = await pool.fetch(
        "SELECT thought_type, count(*) AS n FROM thoughts GROUP BY thought_type ORDER BY n DESC"
    )
    return {
        "total": row["total"],
        "this_week": row["this_week"],
        "today": row["today"],
        "oldest": row["oldest"].isoformat() if row["oldest"] else None,
        "newest": row["newest"].isoformat() if row["newest"] else None,
        "by_type": {r["thought_type"]: r["n"] for r in type_rows},
    }


# ── Temporal / Supersede ────────────────────────────────────────────────────

async def supersede_thought(old_id: str, new_id: str) -> None:
    """Mark an existing thought as superseded by a new one."""
    pool = await get_pool()
    await pool.execute(
        "SELECT supersede_thought($1::uuid, $2::uuid)",
        old_id,
        new_id,
    )
    logger.info("thought_superseded", old_id=old_id, new_id=new_id)


async def get_thought_timeline(
    subject_name: str,
    top_k: int = 20,
) -> list[dict[str, Any]]:
    """Return all thoughts (current and superseded) linked to a subject, in chronological order."""
    pool = await get_pool()
    rows = await pool.fetch(
        """
        SELECT
            t.id, t.content, t.summary, t.thought_type, t.tags, t.source,
            t.metadata, t.created_at, t.is_current, t.valid_from, t.valid_until,
            t.superseded_by
        FROM thoughts t
        JOIN thought_subjects ts ON ts.thought_id = t.id
        WHERE lower(ts.subject_name) = lower($1)
        ORDER BY t.created_at ASC
        LIMIT $2
        """,
        subject_name,
        top_k,
    )
    return [
        {
            "id": str(r["id"]),
            "content": r["content"],
            "summary": r["summary"],
            "thought_type": r["thought_type"],
            "tags": list(r["tags"]),
            "source": r["source"],
            "metadata": r["metadata"],
            "created_at": r["created_at"].isoformat(),
            "is_current": r["is_current"],
            "valid_from": r["valid_from"].isoformat() if r["valid_from"] else None,
            "valid_until": r["valid_until"].isoformat() if r["valid_until"] else None,
            "superseded_by": str(r["superseded_by"]) if r["superseded_by"] else None,
        }
        for r in rows
    ]


async def link_subjects(thought_id: str, subjects: list[dict[str, str]]) -> None:
    """Link a thought to one or more subjects (entity references)."""
    if not subjects:
        return
    pool = await get_pool()
    for subj in subjects:
        await pool.execute(
            """
            INSERT INTO thought_subjects (thought_id, subject_name, subject_type)
            VALUES ($1::uuid, $2, $3)
            """,
            thought_id,
            subj["name"],
            subj.get("type"),
        )
    logger.info("subjects_linked", thought_id=thought_id, count=len(subjects))
