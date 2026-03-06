"""OpenBrain Telegram bot — capture and search thoughts via Telegram.

Single-user bot secured by numeric Telegram user ID.
No OpenClaw dependency — connects directly to OpenBrain.

# TODO(tailscale): Switch from polling to webhook when exposing over Tailscale network.
#                  Add: OPENBRAIN_WEBHOOK_URL + OPENBRAIN_WEBHOOK_SECRET env vars.
#                  Use aiohttp webhook server (see claude-single-remote/webhook_server.py).
"""

from __future__ import annotations

import logging

import structlog
from telegram import Update

from telegram.ext import Application, ContextTypes, MessageHandler, CommandHandler, filters

from .brain import dispatch
from .config import get_config
from .intent import parse

logger = structlog.get_logger(__name__)


def _is_authorized(update: Update, allowed_id: int) -> bool:
    user = update.effective_user
    return user is not None and user.id == allowed_id


async def _handle_message(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    config = get_config()

    if not _is_authorized(update, config.telegram_allowed_user_id):
        await update.message.reply_text("Unauthorized.")
        logger.warning("unauthorized_telegram", user_id=update.effective_user.id if update.effective_user else None)
        return

    text = (update.message.text or "").strip()
    if not text:
        return

    parsed = parse(text)
    response = await dispatch(parsed, source="telegram")

    await update.message.reply_text(response)


async def _cmd_help(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    config = get_config()
    if not _is_authorized(update, config.telegram_allowed_user_id):
        return
    parsed = parse("help")
    response = await dispatch(parsed, source="telegram")
    await update.message.reply_text(response)


async def _cmd_stats(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    config = get_config()
    if not _is_authorized(update, config.telegram_allowed_user_id):
        return
    parsed = parse("stats")
    response = await dispatch(parsed, source="telegram")
    await update.message.reply_text(response)


async def _cmd_review(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    config = get_config()
    if not _is_authorized(update, config.telegram_allowed_user_id):
        return
    parsed = parse("weekly review")
    response = await dispatch(parsed, source="telegram")
    await update.message.reply_text(response)


def run_bot() -> None:
    """Start the Telegram bot in polling mode."""
    config = get_config()

    if not config.telegram_bot_token:
        raise RuntimeError("OPENBRAIN_TELEGRAM_BOT_TOKEN is not set")
    if not config.telegram_allowed_user_id:
        raise RuntimeError("OPENBRAIN_TELEGRAM_ALLOWED_USER_ID is not set")

    logger.info("telegram_bot_starting", polling=True)

    app = (
        Application.builder()
        .token(config.telegram_bot_token)
        .build()
    )

    app.add_handler(CommandHandler("help", _cmd_help))
    app.add_handler(CommandHandler("start", _cmd_help))
    app.add_handler(CommandHandler("stats", _cmd_stats))
    app.add_handler(CommandHandler("review", _cmd_review))
    app.add_handler(MessageHandler(filters.TEXT & ~filters.COMMAND, _handle_message))

    app.run_polling(drop_pending_updates=True)


if __name__ == "__main__":
    run_bot()
