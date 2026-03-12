"""OpenBrain core dispatcher — shared by Telegram bot and web UI.

Takes a ParsedIntent and executes the appropriate OpenBrain action,
returning a plain-text (Markdown-friendly) response string.
"""

from __future__ import annotations

import structlog

from .db import (
    get_stats,
    get_thoughts_since,
    hybrid_search_thoughts,
    insert_thought,
    keyword_search_thoughts,
    link_subjects,
    search_thoughts,
    supersede_thought,
)
from .embeddings import embed
from .intent import HELP_TEXT, Intent, ParsedIntent


def _extract_subjects_simple(text: str, thought_type: str, tags: list[str]) -> list[dict[str, str]]:
    """Extract subject references from text without LLM — tags + basic heuristics."""
    subjects = []
    for tag in tags:
        subjects.append({"name": tag, "type": "tag"})
    if thought_type == "person":
        # Try to grab proper nouns (capitalized words that aren't sentence-start)
        import re
        words = text.split()
        for i, word in enumerate(words):
            cleaned = re.sub(r"[^a-zA-Z']", "", word)
            if cleaned and cleaned[0].isupper() and i > 0:
                subjects.append({"name": cleaned, "type": "person"})
    return subjects

logger = structlog.get_logger(__name__)


async def dispatch(parsed: ParsedIntent, source: str = "web") -> str:
    """Execute a parsed intent and return a human-readable response."""
    if parsed.intent == Intent.HELP:
        return HELP_TEXT

    if parsed.intent == Intent.STATS:
        return await _stats()

    if parsed.intent == Intent.REVIEW:
        return await _review()

    if parsed.intent == Intent.SEARCH:
        return await _search(parsed.text)

    if parsed.intent == Intent.SUPERSEDE:
        return await _supersede(parsed, source)

    if parsed.intent == Intent.EXTRACT:
        return await _deep_capture(parsed, source)

    if parsed.intent == Intent.CAPTURE:
        return await _capture(parsed, source)

    return "I didn't understand that. Type `help` to see what I can do."


async def _capture(parsed: ParsedIntent, source: str) -> str:
    vec = embed(parsed.text)
    tags = parsed.tags or []
    thought_id = await insert_thought(
        content=parsed.text,
        embedding=vec,
        thought_type=parsed.thought_type,
        tags=tags,
        source=source,
    )
    subjects = _extract_subjects_simple(parsed.text, parsed.thought_type, tags)
    if subjects:
        await link_subjects(thought_id, subjects)
    type_label = parsed.thought_type.capitalize()
    return f"Got it. Saved as {type_label}. ({thought_id[:8]})"


async def _supersede(parsed: ParsedIntent, source: str) -> str:
    """Capture a new thought and supersede the most relevant existing one."""
    vec = embed(parsed.text)

    # Find the best current match to supersede
    candidates = await hybrid_search_thoughts(
        query_text=parsed.supersede_query or parsed.text,
        embedding=vec,
        top_k=1,
    )

    # Store the new thought
    tags = parsed.tags or []
    new_id = await insert_thought(
        content=parsed.text,
        embedding=vec,
        thought_type=parsed.thought_type,
        tags=tags,
        source=source,
    )
    subjects = _extract_subjects_simple(parsed.text, parsed.thought_type, tags)
    if subjects:
        await link_subjects(new_id, subjects)

    if candidates and candidates[0]["score"] >= 0.3:
        old = candidates[0]
        await supersede_thought(old["id"], new_id)
        type_label = parsed.thought_type.capitalize()
        return (
            f"Got it. Saved as {type_label}. ({new_id[:8]})\n"
            f"Superseded: {old['content'][:80]}... ({old['id'][:8]})"
        )

    type_label = parsed.thought_type.capitalize()
    return (
        f"Got it. Saved as {type_label}. ({new_id[:8]})\n"
        f"No matching previous thought found to supersede."
    )


async def _deep_capture(parsed: ParsedIntent, source: str) -> str:
    """Extract multiple thoughts from long-form text using LLM, or fall back to single capture."""
    from .config import get_config
    config = get_config()

    if config.extract_provider == "none":
        # Fall back to simple capture
        return await _capture(
            ParsedIntent(intent=Intent.CAPTURE, text=parsed.text, thought_type="note"),
            source,
        )

    try:
        from .extract import extract_thoughts
        candidates = await extract_thoughts(parsed.text)
    except Exception as exc:
        logger.error("extraction_failed", error=str(exc))
        return await _capture(
            ParsedIntent(intent=Intent.CAPTURE, text=parsed.text, thought_type="note"),
            source,
        )

    if not candidates:
        return await _capture(
            ParsedIntent(intent=Intent.CAPTURE, text=parsed.text, thought_type="note"),
            source,
        )

    ids = []
    for c in candidates:
        vec = embed(c["content"])
        thought_id = await insert_thought(
            content=c["content"],
            embedding=vec,
            thought_type=c.get("thought_type", "note"),
            tags=c.get("tags", []),
            source=source,
        )
        ids.append(thought_id)

        # Link subjects from LLM extraction
        if c.get("subjects"):
            await link_subjects(
                thought_id,
                [{"name": s, "type": None} for s in c["subjects"]],
            )

        # Handle supersede if extraction suggests it
        if c.get("supersedes_query"):
            s_vec = embed(c["supersedes_query"])
            old_matches = await hybrid_search_thoughts(
                query_text=c["supersedes_query"],
                embedding=s_vec,
                top_k=1,
            )
            if old_matches and old_matches[0]["score"] >= 0.3:
                await supersede_thought(old_matches[0]["id"], thought_id)

    lines = [f"Extracted {len(candidates)} thought(s):"]
    for c, tid in zip(candidates, ids):
        lines.append(f"- [{c.get('thought_type', 'note')}] {c['content'][:80]}... ({tid[:8]})")
    return "\n".join(lines)


async def _search(query: str, mode: str = "hybrid") -> str:
    if mode == "keyword":
        results = await keyword_search_thoughts(query_text=query, top_k=8)
    elif mode == "vector":
        vec = embed(query)
        results = await search_thoughts(embedding=vec, top_k=8)
    else:
        vec = embed(query)
        results = await hybrid_search_thoughts(query_text=query, embedding=vec, top_k=8)

    if not results:
        stats = await get_stats()
        total = stats.get("total", 0)
        if total == 0:
            return (
                "Your brain is empty — no thoughts stored yet.\n\n"
                "Start capturing with sentences like:\n"
                "- decided to use Python for this project\n"
                "- realised that mornings are my best thinking time\n"
                "- met Alice Chen, she runs design at Acme\n\n"
                "Once you have thoughts stored, search will find them."
            )
        return (
            f"Nothing found matching: {query}\n\n"
            f"Your brain has {total} thought(s) — try a different search term."
        )

    lines = [f"{len(results)} result(s) for: {query}\n"]
    for i, r in enumerate(results, 1):
        date = r["created_at"][:10]
        lines.append(
            f"{i}. [{r['thought_type']}] {date} (score {r['score']})\n"
            f"   {r['content']}"
        )
        if r.get("tags"):
            lines.append(f"   tags: {', '.join(r['tags'])}")
    return "\n".join(lines)


async def _review() -> str:
    thoughts = await get_thoughts_since(7)
    if not thoughts:
        stats = await get_stats()
        total = stats.get("total", 0)
        if total == 0:
            return "Your brain is empty — capture some thoughts first."
        return "No thoughts captured in the last 7 days."

    by_type: dict[str, list] = {}
    for t in thoughts:
        by_type.setdefault(t["thought_type"], []).append(t)

    lines = [f"Weekly Review — {len(thoughts)} thoughts\n"]
    for thought_type, items in sorted(by_type.items()):
        lines.append(f"{thought_type.title()}s ({len(items)})")
        for item in items:
            lines.append(f"- [{item['created_at'][:10]}] {item['content']}")
        lines.append("")
    return "\n".join(lines)


async def _stats() -> str:
    s = await get_stats()
    total = s.get("total", 0)
    if total == 0:
        return "OpenBrain Stats\nYour brain is empty. Start capturing thoughts to see stats here."
    lines = [
        "OpenBrain Stats",
        f"Total: {total} thoughts",
        f"This week: {s['this_week']} · Today: {s['today']}",
    ]
    if s.get("by_type"):
        lines.append("\nBy type:")
        for t, n in s["by_type"].items():
            lines.append(f"  {t}: {n}")
    return "\n".join(lines)
