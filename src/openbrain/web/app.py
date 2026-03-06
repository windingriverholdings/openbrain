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

import structlog
from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from fastapi.responses import HTMLResponse
from fastapi.staticfiles import StaticFiles

from ..brain import dispatch
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
