"""OpenBrain core dispatcher — shared by Telegram bot and web UI.

Takes a ParsedIntent and executes the appropriate OpenBrain action,
returning a plain-text (Markdown-friendly) response string.
"""

from __future__ import annotations

from .db import get_stats, get_thoughts_since, insert_thought, search_thoughts
from .embeddings import embed
from .intent import HELP_TEXT, Intent, ParsedIntent


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

    if parsed.intent == Intent.CAPTURE:
        return await _capture(parsed, source)

    return "I didn't understand that. Type `help` to see what I can do."


async def _capture(parsed: ParsedIntent, source: str) -> str:
    vec = embed(parsed.text)
    thought_id = await insert_thought(
        content=parsed.text,
        embedding=vec,
        thought_type=parsed.thought_type,
        tags=parsed.tags or [],
        source=source,
    )
    type_label = parsed.thought_type.capitalize()
    return f"Got it. Saved as **{type_label}**. `{thought_id[:8]}…`"


async def _search(query: str) -> str:
    vec = embed(query)
    results = await search_thoughts(embedding=vec, top_k=8)

    if not results:
        stats = await get_stats()
        total = stats.get("total", 0)
        if total == 0:
            return (
                "Your brain is empty — no thoughts stored yet.\n\n"
                "Start capturing with sentences like:\n"
                "- *decided to use Python for this project*\n"
                "- *realised that mornings are my best thinking time*\n"
                "- *met Alice Chen, she runs design at Acme*\n\n"
                "Once you have thoughts stored, search will find them."
            )
        return (
            f"Nothing found matching: *{query}*\n\n"
            f"Your brain has **{total}** thought(s) — try a different search term, "
            f"or lower the score threshold in your `.env`."
        )

    lines = [f"**{len(results)} result(s)** for: *{query}*\n"]
    for i, r in enumerate(results, 1):
        date = r["created_at"][:10]
        lines.append(
            f"{i}. `{r['thought_type']}` · {date} · score {r['score']}\n"
            f"   {r['content']}"
        )
        if r.get("tags"):
            lines.append(f"   *{', '.join(r['tags'])}*")
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

    lines = [f"**Weekly Review** — {len(thoughts)} thoughts\n"]
    for thought_type, items in sorted(by_type.items()):
        lines.append(f"**{thought_type.title()}s** ({len(items)})")
        for item in items:
            lines.append(f"- [{item['created_at'][:10]}] {item['content']}")
        lines.append("")
    return "\n".join(lines)


async def _stats() -> str:
    s = await get_stats()
    total = s.get("total", 0)
    if total == 0:
        return (
            "**OpenBrain Stats**\n"
            "Your brain is empty. Start capturing thoughts to see stats here."
        )
    lines = [
        "**OpenBrain Stats**",
        f"Total: **{total}** thoughts",
        f"This week: **{s['this_week']}** · Today: **{s['today']}**",
    ]
    if s.get("by_type"):
        lines.append("\nBy type:")
        for t, n in s["by_type"].items():
            lines.append(f"  `{t}` — {n}")
    return "\n".join(lines)
