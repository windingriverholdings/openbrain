"""OB-078: ambiguous intent must never be silently discarded.

PR #69 added Intent.AMBIGUOUS (openbrain.intent), but only the Go web UI
handled it (an interactive search/save choice card). Every Python caller of
dispatch() -- the Telegram bot, the Slack bot, and the Python web app --
fell through dispatch()'s final "I didn't understand that" branch: an
ambiguous message it should have offered to save was silently discarded, and
the reply was actively misleading (it looked like a parse failure, not an
unresolved choice).

This file pins:
  1. dispatch() routes Intent.AMBIGUOUS to the explicit AMBIGUOUS_PROMPT
     reply, and does so BEFORE reaching a data-touching call (no embed, no
     insert): guessing search-or-capture is exactly the behavior being
     removed, not relocated.
  2. The Telegram bot's message handler forwards that reply verbatim to the
     user via reply_text().
  3. The Slack bot's app_mention and message (DM) handlers forward that
     reply verbatim via say().
"""

from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest

from openbrain.brain import AMBIGUOUS_PROMPT, dispatch
from openbrain.intent import Intent, ParsedIntent


@pytest.mark.asyncio
async def test_dispatch_ambiguous_returns_explicit_prompt() -> None:
    parsed = ParsedIntent(intent=Intent.AMBIGUOUS, text="tent notes", thought_type="note")
    result = await dispatch(parsed, source="telegram")
    assert result == AMBIGUOUS_PROMPT


@pytest.mark.asyncio
async def test_dispatch_ambiguous_mentions_both_explicit_prefixes() -> None:
    """The prompt must tell the user how to disambiguate, using prefixes
    intent.parse() actually recognizes -- or the instruction is a lie."""
    from openbrain.intent import parse

    assert "find:" in AMBIGUOUS_PROMPT
    assert "note:" in AMBIGUOUS_PROMPT
    assert parse("find: tent notes").intent == Intent.SEARCH
    assert parse("note: tent notes").intent == Intent.CAPTURE


@pytest.mark.asyncio
async def test_dispatch_ambiguous_does_not_touch_the_database(monkeypatch: pytest.MonkeyPatch) -> None:
    """Neither insert_thought (a silent capture) nor a search call runs for
    an ambiguous message: proves the fix asks instead of guessing, in
    either direction."""
    insert_called = False
    search_called = False

    async def _fail_insert(*args, **kwargs):
        nonlocal insert_called
        insert_called = True
        raise AssertionError("dispatch() must not capture an ambiguous message")

    async def _fail_search(*args, **kwargs):
        nonlocal search_called
        search_called = True
        raise AssertionError("dispatch() must not search an ambiguous message")

    monkeypatch.setattr("openbrain.brain.insert_thought", _fail_insert)
    monkeypatch.setattr("openbrain.brain.hybrid_search_thoughts", _fail_search)
    monkeypatch.setattr("openbrain.brain.search_thoughts", _fail_search)
    monkeypatch.setattr("openbrain.brain.keyword_search_thoughts", _fail_search)

    parsed = ParsedIntent(intent=Intent.AMBIGUOUS, text="tent notes", thought_type="note")
    result = await dispatch(parsed, source="slack")

    assert result == AMBIGUOUS_PROMPT
    assert insert_called is False
    assert search_called is False


@pytest.mark.asyncio
async def test_telegram_handle_message_replies_with_ambiguous_prompt(monkeypatch: pytest.MonkeyPatch) -> None:
    from openbrain import telegram_bot

    fake_config = SimpleNamespace(telegram_allowed_user_id=42)
    monkeypatch.setattr(telegram_bot, "get_config", lambda: fake_config)

    reply_text = AsyncMock()
    update = SimpleNamespace(
        effective_user=SimpleNamespace(id=42),
        message=SimpleNamespace(text="tent notes", reply_text=reply_text),
    )

    await telegram_bot._handle_message(update, context=None)

    reply_text.assert_awaited_once_with(AMBIGUOUS_PROMPT)


@pytest.mark.asyncio
async def test_slack_app_mention_replies_with_ambiguous_prompt(monkeypatch: pytest.MonkeyPatch) -> None:
    from openbrain import slack_bot

    fake_config = SimpleNamespace(slack_allowed_user_id="U123")
    monkeypatch.setattr(slack_bot, "get_config", lambda: fake_config)

    app = slack_bot._build_app("xoxb-test-fake-token")
    # The mention/DM handlers are closures registered as event listeners
    # (see slack_bolt's AsyncApp.event decorator); _build_app does not
    # expose them by name, so this reaches the real registered function via
    # the listener's ack_function -- the same callable Slack Bolt would
    # invoke for a live event -- rather than re-deriving the logic.
    handle_mention = app._async_listeners[0].ack_function

    say = AsyncMock()
    event = {"user": "U123", "text": "<@BOTID> tent notes"}

    await handle_mention(event=event, say=say)

    say.assert_awaited_once_with(AMBIGUOUS_PROMPT)


@pytest.mark.asyncio
async def test_slack_dm_replies_with_ambiguous_prompt(monkeypatch: pytest.MonkeyPatch) -> None:
    from openbrain import slack_bot

    fake_config = SimpleNamespace(slack_allowed_user_id="U123")
    monkeypatch.setattr(slack_bot, "get_config", lambda: fake_config)

    app = slack_bot._build_app("xoxb-test-fake-token")
    handle_dm = app._async_listeners[1].ack_function

    say = AsyncMock()
    event = {"user": "U123", "text": "tent notes", "channel_type": "im"}

    await handle_dm(event=event, say=say)

    say.assert_awaited_once_with(AMBIGUOUS_PROMPT)
