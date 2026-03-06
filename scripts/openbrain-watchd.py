#!/usr/bin/env python3
"""OpenBrain sandbox file bridge daemon.

Watches /workspace/openbrain-request.json, processes the request via
OpenBrain, and writes the result to /workspace/openbrain-response.json.

Protocol:
  Request:  {"cmd": "search"|"capture"|"stats"|"review", ...args}
  Response: {"ok": true, "result": ...} | {"ok": false, "error": "..."}

Run via systemd: openbrain-watchd.service
"""

from __future__ import annotations

import asyncio
import json
import os
import sys
from pathlib import Path

# Add OpenBrain src to path
REPO_DIR = Path(__file__).parent.parent
sys.path.insert(0, str(REPO_DIR / "src"))

SANDBOX_DIR = Path.home() / ".openclaw/sandboxes"
REQUEST_NAME = "openbrain-request.json"
RESPONSE_NAME = "openbrain-response.json"
POLL_INTERVAL = 0.5  # seconds


def _find_request_file() -> Path | None:
    for candidate in SANDBOX_DIR.glob(f"agent-main-*/{REQUEST_NAME}"):
        return candidate
    return None


async def _handle(req_path: Path) -> None:
    response_path = req_path.parent / RESPONSE_NAME

    try:
        raw = req_path.read_text()
        req = json.loads(raw)
    except Exception as exc:
        response_path.write_text(json.dumps({"ok": False, "error": f"bad request: {exc}"}))
        req_path.unlink(missing_ok=True)
        return

    cmd = req.get("cmd", "")
    try:
        result = await _dispatch(cmd, req)
        response_path.write_text(json.dumps({"ok": True, "result": result}))
    except Exception as exc:
        response_path.write_text(json.dumps({"ok": False, "error": str(exc)}))
    finally:
        req_path.unlink(missing_ok=True)


async def _dispatch(cmd: str, req: dict) -> object:
    from openbrain.db import get_stats, get_thoughts_since, insert_thought, search_thoughts
    from openbrain.embeddings import embed
    from openbrain.config import get_config

    config = get_config()

    if cmd == "search":
        query = req.get("query", "")
        top_k = int(req.get("top_k", 5))
        vec = embed(query)
        results = await search_thoughts(
            embedding=vec,
            top_k=top_k,
            score_threshold=config.search_score_threshold,
        )
        return {"query": query, "count": len(results), "results": results}

    elif cmd == "capture":
        content = req["content"]
        vec = embed(content)
        thought_id = await insert_thought(
            content=content,
            embedding=vec,
            thought_type=req.get("thought_type", "note"),
            tags=req.get("tags", []),
            source=req.get("source", "openclaw"),
            summary=req.get("summary"),
        )
        return {"id": thought_id, "thought_type": req.get("thought_type", "note")}

    elif cmd == "stats":
        return await get_stats()

    elif cmd == "review":
        days = int(req.get("days", 7))
        thoughts = await get_thoughts_since(days)
        by_type: dict[str, list] = {}
        for t in thoughts:
            by_type.setdefault(t["thought_type"], []).append(t)
        return {"days": days, "total": len(thoughts), "by_type": by_type}

    else:
        raise ValueError(f"unknown cmd: {cmd!r}")


async def main() -> None:
    import structlog
    structlog.configure(
        processors=[
            structlog.processors.TimeStamper(fmt="iso"),
            structlog.dev.ConsoleRenderer(),
        ]
    )
    log = structlog.get_logger("openbrain-watchd")
    log.info("watchd_starting", sandbox_dir=str(SANDBOX_DIR), poll=POLL_INTERVAL)

    while True:
        req_path = _find_request_file()
        if req_path and req_path.exists():
            log.info("request_received", path=str(req_path))
            await _handle(req_path)
            log.info("request_handled")
        await asyncio.sleep(POLL_INTERVAL)


if __name__ == "__main__":
    asyncio.run(main())
