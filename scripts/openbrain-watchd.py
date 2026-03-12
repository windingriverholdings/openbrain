#!/usr/bin/env python3
"""OpenBrain sandbox file bridge daemon.

Polls active OpenClaw containers for /tmp/openbrain-request.json via
`docker exec`, dispatches the request to OpenBrain, and writes the result
to the sandbox workspace as openbrain-response.json (readable via the
read-only /workspace bind mount).

Also supports the reverse direction: the host can write
~/.openclaw/openbrain-push-request.json to push data into all active
sandboxes as openbrain-push.json.  A periodic heartbeat pushes brain
stats to openbrain-heartbeat.json in every active sandbox.

Protocol (sandbox -> host):
  Agent writes:  /tmp/openbrain-request.json  (container /tmp, always writable)
  watchd reads:  docker exec <container> cat /tmp/openbrain-request.json
  watchd writes: <sandbox-dir>/openbrain-response.json  (host-side)
  Agent reads:   /workspace/openbrain-response.json  (ro mount, readable)

Protocol (host -> sandbox, push):
  Host writes:   ~/.openclaw/openbrain-push-request.json
  watchd writes: <sandbox-dir>/openbrain-push.json
  Agent reads:   /workspace/openbrain-push.json

Heartbeat (automatic):
  watchd writes: <sandbox-dir>/openbrain-heartbeat.json  every HEARTBEAT_INTERVAL s

Run via systemd: openbrain-watchd.service
"""

from __future__ import annotations

import asyncio
import json
import sys
import time
from pathlib import Path

# Add OpenBrain src to path
REPO_DIR = Path(__file__).parent.parent
sys.path.insert(0, str(REPO_DIR / "src"))

SANDBOX_DIR = Path.home() / ".openclaw/sandboxes"
CONTAINERS_JSON = Path.home() / ".openclaw/sandbox/containers.json"
HOST_REQUEST_PATH = Path.home() / ".openclaw/openbrain-push-request.json"

CONTAINER_REQUEST_PATH = "/tmp/openbrain-request.json"
RESPONSE_NAME = "openbrain-response.json"
PUSH_NAME = "openbrain-push.json"
HEARTBEAT_NAME = "openbrain-heartbeat.json"

POLL_INTERVAL = 0.5       # seconds
HEARTBEAT_INTERVAL = 300  # seconds (5 minutes)


# ---------------------------------------------------------------------------
# Container / sandbox discovery
# ---------------------------------------------------------------------------

def _load_containers() -> list[dict]:
    try:
        data = json.loads(CONTAINERS_JSON.read_text())
        return data.get("entries", [])
    except Exception:
        return []


def _sandbox_dir_for(container_name: str) -> Path:
    """openclaw-sbx-agent-main-<id>  ->  ~/.openclaw/sandboxes/agent-main-<id>"""
    sandbox_name = container_name.removeprefix("openclaw-sbx-")
    return SANDBOX_DIR / sandbox_name


def _find_all_sandbox_dirs() -> list[Path]:
    return [
        p for e in _load_containers()
        if (p := _sandbox_dir_for(e["containerName"])).is_dir()
    ]


# ---------------------------------------------------------------------------
# Docker helpers — use subprocess_exec (no shell, no injection risk)
# ---------------------------------------------------------------------------

async def _container_read(container: str) -> str | None:
    """Read CONTAINER_REQUEST_PATH from the container. Returns None if absent."""
    proc = await asyncio.create_subprocess_exec(
        "docker", "exec", container,
        "cat", CONTAINER_REQUEST_PATH,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.DEVNULL,
    )
    stdout, _ = await proc.communicate()
    return stdout.decode() if proc.returncode == 0 else None


async def _container_delete(container: str) -> None:
    """Remove CONTAINER_REQUEST_PATH from the container."""
    proc = await asyncio.create_subprocess_exec(
        "docker", "exec", container,
        "rm", "-f", CONTAINER_REQUEST_PATH,
        stdout=asyncio.subprocess.DEVNULL,
        stderr=asyncio.subprocess.DEVNULL,
    )
    await proc.communicate()


# ---------------------------------------------------------------------------
# Sandbox-initiated request handler (sandbox -> host -> sandbox)
# ---------------------------------------------------------------------------

async def _handle(container: str, raw: str, sandbox_dir: Path) -> None:
    response_path = sandbox_dir / RESPONSE_NAME

    # Delete request first to prevent re-processing on slow dispatch
    await _container_delete(container)

    try:
        req = json.loads(raw)
    except Exception as exc:
        response_path.write_text(json.dumps({"ok": False, "error": f"bad request: {exc}"}))
        return

    cmd = req.get("cmd", "")
    try:
        result = await _dispatch(cmd, req)
        response_path.write_text(json.dumps({"ok": True, "result": result}))
    except Exception as exc:
        response_path.write_text(json.dumps({"ok": False, "error": str(exc)}))


# ---------------------------------------------------------------------------
# Host-initiated push handler (host -> all sandboxes)
# ---------------------------------------------------------------------------

async def _handle_host_push(log) -> None:
    try:
        raw = HOST_REQUEST_PATH.read_text()
        req = json.loads(raw)
    except Exception as exc:
        log.error("host_push_bad_request", error=str(exc))
        HOST_REQUEST_PATH.unlink(missing_ok=True)
        return

    HOST_REQUEST_PATH.unlink(missing_ok=True)

    cmd = req.get("cmd", "")
    try:
        result = await _dispatch(cmd, req)
        payload = json.dumps({"ok": True, "result": result})
    except Exception as exc:
        payload = json.dumps({"ok": False, "error": str(exc)})

    for sandbox in _find_all_sandbox_dirs():
        (sandbox / PUSH_NAME).write_text(payload)
        log.info("push_delivered", sandbox=sandbox.name)


# ---------------------------------------------------------------------------
# Heartbeat push (host -> all sandboxes, periodic)
# ---------------------------------------------------------------------------

async def _push_heartbeat(log) -> None:
    try:
        from openbrain.db import get_stats
        stats = await get_stats()
        payload = json.dumps({"ok": True, "result": stats})
    except Exception as exc:
        payload = json.dumps({"ok": False, "error": str(exc)})

    sandbox_dirs = _find_all_sandbox_dirs()
    for sandbox in sandbox_dirs:
        (sandbox / HEARTBEAT_NAME).write_text(payload)
    if sandbox_dirs:
        log.info("heartbeat_pushed", sandboxes=len(sandbox_dirs))


# ---------------------------------------------------------------------------
# Command dispatcher (shared by both directions)
# ---------------------------------------------------------------------------

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


# ---------------------------------------------------------------------------
# Main loop
# ---------------------------------------------------------------------------

async def main() -> None:
    import structlog
    structlog.configure(
        processors=[
            structlog.processors.TimeStamper(fmt="iso"),
            structlog.dev.ConsoleRenderer(),
        ]
    )
    log = structlog.get_logger("openbrain-watchd")
    log.info(
        "watchd_starting",
        sandbox_dir=str(SANDBOX_DIR),
        containers_json=str(CONTAINERS_JSON),
        host_request=str(HOST_REQUEST_PATH),
        poll=POLL_INTERVAL,
        heartbeat_interval=HEARTBEAT_INTERVAL,
    )

    last_heartbeat = 0.0

    while True:
        # Direction 1: sandbox-initiated requests (via docker exec on /tmp)
        for entry in _load_containers():
            container = entry["containerName"]
            raw = await _container_read(container)
            if raw:
                sandbox_dir = _sandbox_dir_for(container)
                log.info("request_received", container=container)
                await _handle(container, raw, sandbox_dir)
                log.info("request_handled", container=container)

        # Direction 2: host-initiated push to all sandboxes
        if HOST_REQUEST_PATH.exists():
            log.info("host_push_request_received")
            await _handle_host_push(log)

        # Direction 2 (automatic): periodic heartbeat stats
        now = time.monotonic()
        if now - last_heartbeat >= HEARTBEAT_INTERVAL:
            await _push_heartbeat(log)
            last_heartbeat = now

        await asyncio.sleep(POLL_INTERVAL)


if __name__ == "__main__":
    asyncio.run(main())
