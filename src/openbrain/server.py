"""OpenBrain MCP Server — exposes brain tools to any MCP-compatible client."""

from __future__ import annotations

import json
from typing import Any

import structlog
from mcp.server import Server
from mcp.server.stdio import stdio_server
from mcp.types import (
    Tool,
    TextContent,
    CallToolResult,
)

from .config import get_config
from .db import (
    close_pool,
    get_stats,
    get_thought_timeline,
    get_thoughts_since,
    hybrid_search_thoughts,
    insert_thought,
    keyword_search_thoughts,
    link_subjects,
    search_thoughts,
    supersede_thought,
)
from .embeddings import embed, embed_batch

logger = structlog.get_logger(__name__)

server = Server("openbrain")


# ── Tool definitions ─────────────────────────────────────────────────────────

@server.list_tools()
async def list_tools() -> list[Tool]:
    return [
        Tool(
            name="capture_thought",
            description=(
                "Save a thought, idea, decision, insight, meeting note, or memory "
                "into OpenBrain. The thought is embedded and stored for future retrieval."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "content": {
                        "type": "string",
                        "description": "The full text of the thought to capture.",
                    },
                    "thought_type": {
                        "type": "string",
                        "enum": ["decision", "insight", "person", "meeting", "idea", "note", "memory"],
                        "description": "Category of thought.",
                        "default": "note",
                    },
                    "tags": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Optional tags for filtering (e.g. ['work', 'project-x']).",
                        "default": [],
                    },
                    "summary": {
                        "type": "string",
                        "description": "Optional one-line summary for quick display.",
                    },
                    "source": {
                        "type": "string",
                        "description": "Interface this thought came from (telegram, claude, web, cli, import).",
                        "default": "claude",
                    },
                    "metadata": {
                        "type": "object",
                        "description": "Optional key-value metadata (e.g. {\"person\": \"Alice\", \"project\": \"OpenBrain\"}).",
                        "default": {},
                    },
                },
                "required": ["content"],
            },
        ),
        Tool(
            name="search_thoughts",
            description=(
                "Search OpenBrain for thoughts related to a query. "
                "Default mode is hybrid (keyword + semantic). "
                "Use 'vector' for pure semantic or 'keyword' for exact term matching."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "query": {
                        "type": "string",
                        "description": "Natural language search query.",
                    },
                    "top_k": {
                        "type": "integer",
                        "description": "Maximum number of results to return.",
                        "default": 10,
                    },
                    "mode": {
                        "type": "string",
                        "enum": ["hybrid", "vector", "keyword"],
                        "description": "Search mode: hybrid (keyword+semantic, default), vector (semantic only), keyword (full-text only).",
                        "default": "hybrid",
                    },
                    "thought_type": {
                        "type": "string",
                        "enum": ["decision", "insight", "person", "meeting", "idea", "note", "memory"],
                        "description": "Filter by thought type (optional).",
                    },
                    "tags": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Filter to thoughts that have ANY of these tags (optional).",
                    },
                    "include_history": {
                        "type": "boolean",
                        "description": "Include superseded thoughts (default: false, only current thoughts).",
                        "default": False,
                    },
                },
                "required": ["query"],
            },
        ),
        Tool(
            name="weekly_review",
            description=(
                "Retrieve all thoughts from the past N days, grouped by type. "
                "Use this to synthesise a weekly review — find connections, gaps, and patterns."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "days": {
                        "type": "integer",
                        "description": "How many days back to look (default: 7).",
                        "default": 7,
                    },
                },
            },
        ),
        Tool(
            name="brain_stats",
            description="Return aggregate statistics about the OpenBrain knowledge base.",
            inputSchema={"type": "object", "properties": {}},
        ),
        Tool(
            name="bulk_import",
            description=(
                "Import multiple thoughts at once (e.g. from a memory migration). "
                "Accepts a list of thought objects and embeds them all efficiently."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "thoughts": {
                        "type": "array",
                        "items": {
                            "type": "object",
                            "properties": {
                                "content": {"type": "string"},
                                "thought_type": {"type": "string", "default": "memory"},
                                "tags": {"type": "array", "items": {"type": "string"}},
                                "summary": {"type": "string"},
                                "metadata": {"type": "object"},
                            },
                            "required": ["content"],
                        },
                        "description": "List of thoughts to import.",
                    },
                    "source": {
                        "type": "string",
                        "description": "Import source label (e.g. 'import', 'migration').",
                        "default": "import",
                    },
                },
                "required": ["thoughts"],
            },
        ),
        Tool(
            name="thought_timeline",
            description=(
                "Return the full history of thoughts about a subject, including superseded ones. "
                "Shows how knowledge about a person, tool, or concept evolved over time."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "subject": {
                        "type": "string",
                        "description": "Subject name to get timeline for (person, tool, concept, etc.).",
                    },
                    "top_k": {
                        "type": "integer",
                        "description": "Maximum number of timeline entries.",
                        "default": 20,
                    },
                },
                "required": ["subject"],
            },
        ),
        Tool(
            name="extract_thoughts",
            description=(
                "Extract multiple structured thoughts from long-form text using LLM. "
                "Pass in meeting notes, a conversation, or a long note and get back "
                "individual typed thoughts with tags and subject links. "
                "Requires LLM extraction to be enabled (OPENBRAIN_EXTRACT_PROVIDER != none)."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "text": {
                        "type": "string",
                        "description": "The long-form text to extract thoughts from.",
                    },
                    "auto_capture": {
                        "type": "boolean",
                        "description": "If true, automatically capture extracted thoughts. If false, return candidates only.",
                        "default": False,
                    },
                    "source": {
                        "type": "string",
                        "description": "Source label for captured thoughts.",
                        "default": "claude",
                    },
                },
                "required": ["text"],
            },
        ),
        Tool(
            name="supersede_thought",
            description=(
                "Capture a new thought and mark an older thought as superseded. "
                "Use when you have updated knowledge that replaces a previous belief, "
                "decision, or fact. Provide old_thought_id to supersede directly, or "
                "let OpenBrain find the best match via supersedes_query."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "content": {
                        "type": "string",
                        "description": "The new thought that replaces the old one.",
                    },
                    "supersedes_query": {
                        "type": "string",
                        "description": (
                            "Search query to find the thought being superseded. "
                            "If omitted, the new content itself is used as the query."
                        ),
                    },
                    "old_thought_id": {
                        "type": "string",
                        "description": (
                            "Explicit UUID of the thought to supersede. "
                            "If provided, skips the search entirely."
                        ),
                    },
                    "thought_type": {
                        "type": "string",
                        "enum": ["decision", "insight", "person", "meeting", "idea", "note", "memory"],
                        "description": "Category for the new thought.",
                        "default": "note",
                    },
                    "tags": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Tags for the new thought.",
                        "default": [],
                    },
                    "source": {
                        "type": "string",
                        "description": "Interface this thought came from.",
                        "default": "claude",
                    },
                    "summary": {
                        "type": "string",
                        "description": "Optional one-line summary for the new thought.",
                    },
                },
                "required": ["content"],
            },
        ),
    ]


# ── Tool handlers ─────────────────────────────────────────────────────────────

@server.call_tool()
async def call_tool(name: str, arguments: dict[str, Any]) -> CallToolResult:
    try:
        if name == "capture_thought":
            return await _capture_thought(arguments)
        elif name == "search_thoughts":
            return await _search_thoughts(arguments)
        elif name == "weekly_review":
            return await _weekly_review(arguments)
        elif name == "brain_stats":
            return await _brain_stats()
        elif name == "bulk_import":
            return await _bulk_import(arguments)
        elif name == "thought_timeline":
            return await _thought_timeline(arguments)
        elif name == "extract_thoughts":
            return await _extract_thoughts(arguments)
        elif name == "supersede_thought":
            return await _supersede_thought(arguments)
        else:
            return CallToolResult(
                content=[TextContent(type="text", text=f"Unknown tool: {name}")],
                isError=True,
            )
    except Exception as exc:
        logger.error("tool_error", tool=name, error=str(exc))
        return CallToolResult(
            content=[TextContent(type="text", text=f"Error: {exc}")],
            isError=True,
        )


async def _capture_thought(args: dict[str, Any]) -> CallToolResult:
    content = args["content"]
    vec = embed(content)
    thought_id = await insert_thought(
        content=content,
        embedding=vec,
        thought_type=args.get("thought_type", "note"),
        tags=args.get("tags", []),
        source=args.get("source", "claude"),
        summary=args.get("summary"),
        metadata=args.get("metadata", {}),
    )
    return CallToolResult(
        content=[
            TextContent(
                type="text",
                text=f"Thought captured. ID: {thought_id}",
            )
        ]
    )


async def _search_thoughts(args: dict[str, Any]) -> CallToolResult:
    config = get_config()
    query = args["query"]
    top_k = args.get("top_k", config.search_top_k)
    mode = args.get("mode", "hybrid")

    include_history = args.get("include_history", False)

    if mode == "keyword":
        results = await keyword_search_thoughts(
            query_text=query, top_k=top_k, include_history=include_history,
        )
    elif mode == "vector":
        vec = embed(query)
        results = await search_thoughts(
            embedding=vec,
            top_k=top_k,
            thought_type=args.get("thought_type"),
            tags=args.get("tags"),
            score_threshold=config.search_score_threshold,
        )
    else:
        vec = embed(query)
        results = await hybrid_search_thoughts(
            query_text=query,
            embedding=vec,
            top_k=top_k,
            include_history=include_history,
        )
    if not results:
        return CallToolResult(
            content=[TextContent(type="text", text="No matching thoughts found.")]
        )
    lines = [f"Found {len(results)} thought(s) related to: \"{query}\"\n"]
    for i, r in enumerate(results, 1):
        lines.append(
            f"{i}. [{r['thought_type']}] (score: {r['score']}) — {r['created_at'][:10]}\n"
            f"   {r['content']}"
        )
        if r.get("tags"):
            lines.append(f"   Tags: {', '.join(r['tags'])}")
        lines.append("")
    return CallToolResult(
        content=[TextContent(type="text", text="\n".join(lines))]
    )


async def _weekly_review(args: dict[str, Any]) -> CallToolResult:
    days = args.get("days", 7)
    thoughts = await get_thoughts_since(days)
    if not thoughts:
        return CallToolResult(
            content=[TextContent(type="text", text=f"No thoughts captured in the last {days} days.")]
        )

    by_type: dict[str, list[dict]] = {}
    for t in thoughts:
        key = t["thought_type"]
        by_type.setdefault(key, []).append(t)

    lines = [f"# Weekly Review — last {days} days ({len(thoughts)} thoughts)\n"]
    for thought_type, items in sorted(by_type.items()):
        lines.append(f"## {thought_type.title()}s ({len(items)})")
        for item in items:
            date = item["created_at"][:10]
            lines.append(f"- [{date}] {item['content']}")
        lines.append("")

    return CallToolResult(
        content=[TextContent(type="text", text="\n".join(lines))]
    )


async def _brain_stats() -> CallToolResult:
    stats = await get_stats()
    text = (
        f"OpenBrain Statistics\n"
        f"━━━━━━━━━━━━━━━━━━━━\n"
        f"Total thoughts : {stats['total']}\n"
        f"This week      : {stats['this_week']}\n"
        f"Today          : {stats['today']}\n"
        f"Oldest thought : {stats['oldest'] or 'n/a'}\n"
        f"Newest thought : {stats['newest'] or 'n/a'}\n\n"
        f"By type:\n"
    )
    for t, n in (stats.get("by_type") or {}).items():
        text += f"  {t:<12} {n}\n"
    return CallToolResult(content=[TextContent(type="text", text=text)])


async def _bulk_import(args: dict[str, Any]) -> CallToolResult:
    thoughts = args["thoughts"]
    source = args.get("source", "import")

    contents = [t["content"] for t in thoughts]
    embeddings = embed_batch(contents)

    ids = []
    for thought, vec in zip(thoughts, embeddings):
        thought_id = await insert_thought(
            content=thought["content"],
            embedding=vec,
            thought_type=thought.get("thought_type", "memory"),
            tags=thought.get("tags", []),
            source=source,
            summary=thought.get("summary"),
            metadata=thought.get("metadata", {}),
        )
        ids.append(thought_id)

    return CallToolResult(
        content=[
            TextContent(
                type="text",
                text=f"Imported {len(ids)} thoughts. IDs: {', '.join(ids[:5])}"
                + (" ..." if len(ids) > 5 else ""),
            )
        ]
    )


async def _extract_thoughts(args: dict[str, Any]) -> CallToolResult:
    from .extract import extract_thoughts

    text = args["text"]
    auto_capture = args.get("auto_capture", False)
    source = args.get("source", "claude")

    candidates = await extract_thoughts(text)
    if not candidates:
        return CallToolResult(
            content=[TextContent(type="text", text="No thoughts could be extracted from the input.")]
        )

    if not auto_capture:
        lines = [f"Extracted {len(candidates)} thought candidate(s):\n"]
        for i, c in enumerate(candidates, 1):
            lines.append(
                f"{i}. [{c['thought_type']}] {c['content']}\n"
                f"   tags: {', '.join(c.get('tags', []))}"
            )
            if c.get("subjects"):
                lines.append(f"   subjects: {', '.join(c['subjects'])}")
            if c.get("supersedes_query"):
                lines.append(f"   supersedes: \"{c['supersedes_query']}\"")
            lines.append("")
        lines.append("Use capture_thought to save individual candidates, or call again with auto_capture=true.")
        return CallToolResult(
            content=[TextContent(type="text", text="\n".join(lines))]
        )

    # Auto-capture all candidates
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
        if c.get("subjects"):
            await link_subjects(
                thought_id,
                [{"name": s, "type": None} for s in c["subjects"]],
            )

    lines = [f"Extracted and captured {len(ids)} thought(s):"]
    for c, tid in zip(candidates, ids):
        lines.append(f"- [{c['thought_type']}] {c['content'][:80]}... ({tid[:8]})")
    return CallToolResult(
        content=[TextContent(type="text", text="\n".join(lines))]
    )


async def _thought_timeline(args: dict[str, Any]) -> CallToolResult:
    subject = args["subject"]
    top_k = args.get("top_k", 20)
    timeline = await get_thought_timeline(subject, top_k=top_k)
    if not timeline:
        return CallToolResult(
            content=[TextContent(type="text", text=f"No thoughts found for subject: {subject}")]
        )
    lines = [f"Timeline for \"{subject}\" ({len(timeline)} entries)\n"]
    for i, t in enumerate(timeline, 1):
        status = "CURRENT" if t["is_current"] else "SUPERSEDED"
        date = t["created_at"][:10]
        lines.append(
            f"{i}. [{status}] [{t['thought_type']}] {date}\n"
            f"   {t['content']}"
        )
        if t.get("superseded_by"):
            lines.append(f"   → superseded by {t['superseded_by'][:8]}")
        lines.append("")
    return CallToolResult(
        content=[TextContent(type="text", text="\n".join(lines))]
    )


async def _supersede_thought(args: dict[str, Any]) -> CallToolResult:
    content = args["content"]
    vec = embed(content)

    new_id = await insert_thought(
        content=content,
        embedding=vec,
        thought_type=args.get("thought_type", "note"),
        tags=args.get("tags", []),
        source=args.get("source", "claude"),
        summary=args.get("summary"),
        metadata={},
    )

    old_thought_id = args.get("old_thought_id")
    if old_thought_id:
        await supersede_thought(old_thought_id, new_id)
        return CallToolResult(
            content=[TextContent(
                type="text",
                text=f"New thought saved. ID: {new_id}\nSuperseded: {old_thought_id}",
            )]
        )

    query = args.get("supersedes_query") or content
    query_vec = embed(query) if args.get("supersedes_query") else vec
    candidates = await hybrid_search_thoughts(
        query_text=query,
        embedding=query_vec,
        top_k=1,
    )

    if candidates and candidates[0]["score"] >= 0.3:
        old = candidates[0]
        await supersede_thought(old["id"], new_id)
        return CallToolResult(
            content=[TextContent(
                type="text",
                text=(
                    f"New thought saved. ID: {new_id}\n"
                    f"Superseded: {old['content'][:80]}... ({old['id'][:8]})"
                ),
            )]
        )

    return CallToolResult(
        content=[TextContent(
            type="text",
            text=(
                f"New thought saved. ID: {new_id}\n"
                "No matching thought found to supersede "
                "(try providing old_thought_id directly)."
            ),
        )]
    )


# ── Entry point ───────────────────────────────────────────────────────────────

async def main() -> None:
    import structlog
    structlog.configure(
        processors=[
            structlog.processors.TimeStamper(fmt="iso"),
            structlog.dev.ConsoleRenderer(),
        ]
    )
    config = get_config()
    logger.info(
        "openbrain_starting",
        server=config.mcp_server_name,
        version=config.mcp_server_version,
        db=config.db_name,
        embedding_model=config.embedding_model,
    )
    async with stdio_server() as (read_stream, write_stream):
        await server.run(read_stream, write_stream, server.create_initialization_options())


if __name__ == "__main__":
    import asyncio
    asyncio.run(main())
