# OpenBrain — Roadmap & TODOs

Items tagged by theme. Each has a pointer to where the work happens.

---

## Tailscale / Remote Access

> Pre-written config exists (`deploy/caddy-tailscale.conf`) — just needs wiring.

- [ ] Change `OPENBRAIN_WEB_HOST` from `127.0.0.1` → `0.0.0.0` when exposing via Tailscale (`deploy/openbrain-web.service`)
- [ ] Fill in `deploy/caddy-tailscale.conf` with your Tailscale hostname and copy to `/etc/caddy/Caddyfile`
- [ ] Add token auth check in the WebSocket handshake (`src/openbrain/web/app.py`)
- [ ] Add rate limiting (slowapi) on web endpoints if exposing beyond localhost (`src/openbrain/web/app.py`)
- [ ] Switch Telegram bot from polling → webhook for lower latency (`src/openbrain/telegram_bot.py`)
- [ ] Add `OPENBRAIN_WEBHOOK_URL` + `OPENBRAIN_WEBHOOK_SECRET` env vars for Telegram webhook mode

---

## Intelligence / LLM

- [ ] Replace regex intent matching in `intent.py` with a local LLM classifier (e.g. Ollama + llama3) for richer NL understanding
- [ ] Auto-summarise captured thoughts at insert time (populate `summary` column)
- [ ] Cluster weekly review results by topic using embeddings (currently just grouped by `thought_type`)
- [ ] Confidence score on intent classification — fall back to clarification prompt when uncertain

---

## Interfaces

- [ ] Claude Desktop app config (once installed — same MCP server, different registration)
- [ ] HTTP/SSE MCP transport for claude.ai web interface (currently stdio only)
- [ ] Discord bot (same brain.py dispatcher, different transport)
- [ ] Browser extension for one-click capture from any web page

---

## Storage / Search

- [ ] Full-text fallback search using `pg_trgm` when vector score is below threshold
- [ ] Tag-based filtering in search (`search_thoughts(query, tags=["work"])`)
- [ ] Thought deduplication on insert — detect near-duplicate embeddings before saving
- [ ] Export to JSON / Markdown for backup or migration

---

## OpenClaw Integration (personal branch)

- [ ] Auto-capture after every OpenClaw session ends (not just when agent judges it worthy)
- [ ] Pre-search OpenBrain at gateway level before handing message to any agent (currently agent-level only)
- [ ] Heartbeat: periodic OpenBrain stats check surfaced in OpenClaw's HEARTBEAT.md rotation

---

## Ops

- [ ] Database backup script (pg_dump → encrypted archive)
- [ ] `pixi run migrate` command for future schema changes
- [ ] Health check endpoint for uptime monitoring (`/health` exists, needs external poller)
- [ ] Log rotation for systemd journal

