"""OpenBrain CLI — standalone interface for work environments without OpenClaw.

Provides direct command-line access to all OpenBrain tools:
  openbrain capture "My thought here" --type decision --tags work,project-x
  openbrain search "what did I decide about the API?"
  openbrain review --days 7
  openbrain stats
  openbrain import thoughts.json
"""

from __future__ import annotations

import asyncio
import json
import sys
from pathlib import Path

import structlog

logger = structlog.get_logger(__name__)


def _setup_logging() -> None:
    import structlog
    structlog.configure(
        processors=[
            structlog.processors.TimeStamper(fmt="iso"),
            structlog.dev.ConsoleRenderer(),
        ]
    )


async def cmd_capture(args: list[str]) -> None:
    from .db import insert_thought
    from .embeddings import embed

    content = " ".join(args) if args else None
    if not content:
        print("Usage: openbrain capture <text> [--type TYPE] [--tags tag1,tag2] [--source SOURCE]")
        sys.exit(1)

    # Parse simple flags
    thought_type = "note"
    tags: list[str] = []
    source = "cli"

    i = 0
    text_parts = []
    while i < len(args):
        if args[i] == "--type" and i + 1 < len(args):
            thought_type = args[i + 1]
            i += 2
        elif args[i] == "--tags" and i + 1 < len(args):
            tags = [t.strip() for t in args[i + 1].split(",")]
            i += 2
        elif args[i] == "--source" and i + 1 < len(args):
            source = args[i + 1]
            i += 2
        else:
            text_parts.append(args[i])
            i += 1

    content = " ".join(text_parts)
    vec = embed(content)
    thought_id = await insert_thought(
        content=content,
        embedding=vec,
        thought_type=thought_type,
        tags=tags,
        source=source,
    )
    print(f"Captured. ID: {thought_id}")


async def cmd_search(args: list[str]) -> None:
    from .config import get_config
    from .db import search_thoughts
    from .embeddings import embed

    if not args:
        print("Usage: openbrain search <query>")
        sys.exit(1)

    query = " ".join(args)
    config = get_config()
    vec = embed(query)
    results = await search_thoughts(
        embedding=vec,
        top_k=config.search_top_k,
        score_threshold=config.search_score_threshold,
    )

    if not results:
        print("No matching thoughts found.")
        return

    print(f"\nFound {len(results)} result(s) for: \"{query}\"\n")
    for i, r in enumerate(results, 1):
        print(f"{i}. [{r['thought_type']}] score={r['score']} — {r['created_at'][:10]}")
        print(f"   {r['content']}")
        if r.get("tags"):
            print(f"   tags: {', '.join(r['tags'])}")
        print()


async def cmd_review(args: list[str]) -> None:
    from .db import get_thoughts_since

    days = 7
    if "--days" in args:
        idx = args.index("--days")
        if idx + 1 < len(args):
            days = int(args[idx + 1])

    thoughts = await get_thoughts_since(days)
    if not thoughts:
        print(f"No thoughts in the last {days} days.")
        return

    by_type: dict[str, list] = {}
    for t in thoughts:
        by_type.setdefault(t["thought_type"], []).append(t)

    print(f"\n# Weekly Review — last {days} days ({len(thoughts)} thoughts)\n")
    for thought_type, items in sorted(by_type.items()):
        print(f"## {thought_type.title()}s ({len(items)})")
        for item in items:
            print(f"  - [{item['created_at'][:10]}] {item['content']}")
        print()


async def cmd_stats(_args: list[str]) -> None:
    from .db import get_stats

    stats = await get_stats()
    print(f"\nOpenBrain Statistics")
    print(f"{'─' * 30}")
    print(f"Total thoughts : {stats['total']}")
    print(f"This week      : {stats['this_week']}")
    print(f"Today          : {stats['today']}")
    print(f"Oldest         : {stats['oldest'] or 'n/a'}")
    print(f"Newest         : {stats['newest'] or 'n/a'}")
    print("\nBy type:")
    for t, n in (stats.get("by_type") or {}).items():
        print(f"  {t:<14} {n}")
    print()


async def cmd_import(args: list[str]) -> None:
    from .db import insert_thought
    from .embeddings import embed_batch

    if not args:
        print("Usage: openbrain import <thoughts.json>")
        sys.exit(1)

    path = Path(args[0])
    if not path.exists():
        print(f"File not found: {path}")
        sys.exit(1)

    thoughts = json.loads(path.read_text())
    if not isinstance(thoughts, list):
        thoughts = [thoughts]

    contents = [t["content"] for t in thoughts]
    embeddings = embed_batch(contents)

    ids = []
    for thought, vec in zip(thoughts, embeddings):
        thought_id = await insert_thought(
            content=thought["content"],
            embedding=vec,
            thought_type=thought.get("thought_type", "memory"),
            tags=thought.get("tags", []),
            source=thought.get("source", "import"),
            summary=thought.get("summary"),
            metadata=thought.get("metadata", {}),
        )
        ids.append(thought_id)

    print(f"Imported {len(ids)} thoughts.")


def main() -> None:
    _setup_logging()

    args = sys.argv[1:]
    if not args:
        print(__doc__)
        sys.exit(0)

    cmd = args[0]
    rest = args[1:]

    commands = {
        "capture": cmd_capture,
        "search": cmd_search,
        "review": cmd_review,
        "stats": cmd_stats,
        "import": cmd_import,
    }

    if cmd not in commands:
        print(f"Unknown command: {cmd}")
        print(f"Available: {', '.join(commands)}")
        sys.exit(1)

    asyncio.run(commands[cmd](rest))


if __name__ == "__main__":
    main()
