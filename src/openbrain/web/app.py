"""OpenBrain web chat server — local-only FastAPI app.

Serves the chat UI at http://mybrain.local:10203
Uses WebSockets for real-time message exchange.

# TODO(tailscale): To expose over Tailscale:
#   1. Change OPENBRAIN_WEB_HOST from 127.0.0.1 to 0.0.0.0 in .env
#   2. Put Caddy in front (see deploy/caddy-tailscale.conf) for TLS
#   3. Add token auth check in the WebSocket handshake (query param or header)
#   4. Consider slowapi rate limiting if exposing beyond localhost
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Optional

import structlog
from fastapi import FastAPI, Query, WebSocket, WebSocketDisconnect
from fastapi.responses import HTMLResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel

from ..brain import dispatch
from ..config import get_config
from ..db import (
    get_stats,
    get_thought_timeline,
    get_thoughts_since,
    hybrid_search_thoughts,
    insert_thought,
    keyword_search_thoughts,
    search_thoughts,
)
from ..embeddings import embed
from ..intent import parse

logger = structlog.get_logger(__name__)

STATIC_DIR = Path(__file__).parent / "static"

app = FastAPI(title="OpenBrain", docs_url=None, redoc_url=None)
app.mount("/static", StaticFiles(directory=str(STATIC_DIR)), name="static")


@app.get("/", response_class=HTMLResponse)
async def index() -> HTMLResponse:
    return HTMLResponse((STATIC_DIR / "index.html").read_text())


@app.get("/health")
async def health() -> dict:
    return {"status": "ok", "service": "openbrain-web"}


# ── REST API (for sandbox / scripted access) ─────────────────────────────────

class CaptureRequest(BaseModel):
    content: str
    thought_type: str = "note"
    tags: list[str] = []
    source: str = "api"
    summary: Optional[str] = None


@app.get("/api/search")
async def api_search(
    q: str = Query(..., description="Natural language search query"),
    top_k: int = Query(5, ge=1, le=50),
    mode: str = Query("hybrid", description="Search mode: hybrid, vector, keyword"),
    include_history: bool = Query(False, description="Include superseded thoughts"),
) -> dict:
    config = get_config()
    if mode == "keyword":
        results = await keyword_search_thoughts(
            query_text=q, top_k=top_k, include_history=include_history,
        )
    elif mode == "vector":
        vec = embed(q)
        results = await search_thoughts(
            embedding=vec,
            top_k=top_k,
            score_threshold=config.search_score_threshold,
        )
    else:
        vec = embed(q)
        results = await hybrid_search_thoughts(
            query_text=q,
            embedding=vec,
            top_k=top_k,
            include_history=include_history,
        )
    return {"query": q, "mode": mode, "count": len(results), "results": results}


@app.post("/api/capture")
async def api_capture(req: CaptureRequest) -> dict:
    vec = embed(req.content)
    thought_id = await insert_thought(
        content=req.content,
        embedding=vec,
        thought_type=req.thought_type,
        tags=req.tags,
        source=req.source,
        summary=req.summary,
    )
    return {"id": thought_id, "thought_type": req.thought_type}


@app.get("/api/stats")
async def api_stats() -> dict:
    return await get_stats()


@app.get("/api/timeline")
async def api_timeline(
    subject: str = Query(..., description="Subject name to get timeline for"),
    top_k: int = Query(20, ge=1, le=100),
) -> dict:
    timeline = await get_thought_timeline(subject, top_k=top_k)
    return {"subject": subject, "count": len(timeline), "timeline": timeline}


@app.get("/api/review")
async def api_review(days: int = Query(7, ge=1, le=365)) -> dict:
    thoughts = await get_thoughts_since(days)
    by_type: dict[str, list] = {}
    for t in thoughts:
        by_type.setdefault(t["thought_type"], []).append(t)
    return {"days": days, "total": len(thoughts), "by_type": by_type}


@app.websocket("/ws")
async def websocket_endpoint(websocket: WebSocket) -> None:
    await websocket.accept()
    logger.info("websocket_connected")
    try:
        while True:
            raw = await websocket.receive_text()
            data = json.loads(raw)
            message = data.get("message", "").strip()
            if not message:
                continue

            parsed = parse(message)
            response = await dispatch(parsed, source="web")

            await websocket.send_text(json.dumps({
                "type": "response",
                "content": response,
                "intent": parsed.intent.value,
                "thought_type": parsed.thought_type,
            }))
    except WebSocketDisconnect:
        logger.info("websocket_disconnected")
    except Exception as exc:
        logger.error("websocket_error", error=str(exc))
        try:
            await websocket.send_text(json.dumps({
                "type": "error",
                "content": f"Error: {exc}",
                "intent": "error",
                "thought_type": "",
            }))
        except Exception:
            pass


def run_server() -> None:
    import uvicorn
    from ..config import get_config
    import structlog
    structlog.configure(
        processors=[
            structlog.processors.TimeStamper(fmt="iso"),
            structlog.dev.ConsoleRenderer(),
        ]
    )
    config = get_config()
    logger.info(
        "openbrain_web_starting",
        host=config.web_host,
        port=config.web_port,
        url=f"http://mybrain.local:{config.web_port}",
    )
    uvicorn.run(app, host=config.web_host, port=config.web_port, log_level="warning")


if __name__ == "__main__":
    run_server()
