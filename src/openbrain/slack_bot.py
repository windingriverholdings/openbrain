"""OpenBrain Slack bot — capture and search thoughts via Slack.

Single-user bot secured by Slack user ID.
Uses Socket Mode (no public URL required — runs entirely local).

Setup:
  1. Create a Slack app at https://api.slack.com/apps
  2. Enable Socket Mode and generate an App-Level Token (xapp-...)
  3. Add bot scopes: app_mentions:read, im:history, im:read, chat:write
  4. Install the app to your workspace and copy the Bot Token (xoxb-...)
  5. Enable the "messages.im" and "app_mention" event subscriptions
  6. Set in .env:
       OPENBRAIN_SLACK_BOT_TOKEN=xoxb-...
       OPENBRAIN_SLACK_APP_TOKEN=xapp-...
       OPENBRAIN_SLACK_ALLOWED_USER_ID=U012AB3CD
"""

from __future__ import annotations

import asyncio
import logging

import structlog
from slack_bolt.async_app import AsyncApp
from slack_bolt.adapter.socket_mode.async_handler import AsyncSocketModeHandler

from .brain import dispatch
from .config import get_config
from .intent import parse

logger = structlog.get_logger(__name__)
_stdlib_logger = logging.getLogger(__name__)


def _is_authorized(user_id: str, allowed_id: str) -> bool:
    return bool(allowed_id) and user_id == allowed_id


def _build_app(bot_token: str) -> AsyncApp:
    app = AsyncApp(token=bot_token, logger=_stdlib_logger)

    @app.event("app_mention")
    async def handle_mention(event: dict, say) -> None:
        """Respond when @mentioned in a channel."""
        config = get_config()
        user_id = event.get("user", "")

        if not _is_authorized(user_id, config.slack_allowed_user_id):
            logger.warning("unauthorized_slack", user_id=user_id)
            await say("Unauthorized.")
            return

        # Strip the @mention prefix from the text
        text = event.get("text", "")
        text = _strip_mention(text).strip()
        if not text:
            return

        parsed = parse(text)
        response = await dispatch(parsed, source="slack")
        await say(response)

    @app.event("message")
    async def handle_dm(event: dict, say) -> None:
        """Respond to direct messages."""
        # Only handle DMs (channel_type = "im"), ignore bot messages
        if event.get("channel_type") != "im":
            return
        if event.get("bot_id") or event.get("subtype"):
            return

        config = get_config()
        user_id = event.get("user", "")

        if not _is_authorized(user_id, config.slack_allowed_user_id):
            logger.warning("unauthorized_slack_dm", user_id=user_id)
            await say("Unauthorized.")
            return

        text = (event.get("text") or "").strip()
        if not text:
            return

        parsed = parse(text)
        response = await dispatch(parsed, source="slack")
        await say(response)

    return app


def _strip_mention(text: str) -> str:
    """Remove leading <@UXXXXX> mention token from Slack message text."""
    import re
    return re.sub(r"^<@[A-Z0-9]+>\s*", "", text)


async def _run(bot_token: str, app_token: str) -> None:
    app = _build_app(bot_token)
    handler = AsyncSocketModeHandler(app, app_token)
    logger.info("slack_bot_starting", mode="socket")
    await handler.start_async()


def run_bot() -> None:
    config = get_config()

    if not config.slack_bot_token:
        raise RuntimeError("OPENBRAIN_SLACK_BOT_TOKEN is not set")
    if not config.slack_app_token:
        raise RuntimeError("OPENBRAIN_SLACK_APP_TOKEN is not set")
    if not config.slack_allowed_user_id:
        raise RuntimeError("OPENBRAIN_SLACK_ALLOWED_USER_ID is not set")

    asyncio.run(_run(config.slack_bot_token, config.slack_app_token))


if __name__ == "__main__":
    run_bot()
