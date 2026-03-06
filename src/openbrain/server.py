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
from .db import close_pool, get_stats, get_thoughts_since, insert_thought, search_thoughts
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
                "Semantically search OpenBrain for thoughts related to a query. "
                "Returns the most relevant thoughts ranked by cosine similarity."
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
    vec = embed(query)
    results = await search_thoughts(
        embedding=vec,
        top_k=args.get("top_k", config.search_top_k),
        thought_type=args.get("thought_type"),
        tags=args.get("tags"),
        score_threshold=config.search_score_threshold,
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
