"""First Python tests for the shared intent classifier (openbrain.intent).

OB-078: the Go and Python intent classifiers are hand-kept in sync (see
scripts/generate_fixtures.py, which dumps Python's parse() output to
testdata/intent_cases.json for the Go suite to replay against). Until now
nothing replayed that same fixture against the Python implementation itself,
so a change to intent.py could silently drift from the fixture (and from Go)
with no Python-side test to catch it. This file closes that gap and adds
direct coverage of _looks_ambiguous, the source of the OB-078 capture-loss
bug on non-web callers.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from openbrain.intent import Intent, _looks_ambiguous, parse

_TESTDATA = Path(__file__).resolve().parent.parent / "testdata" / "intent_cases.json"


def _load_cases() -> list[dict]:
    return json.loads(_TESTDATA.read_text())


@pytest.mark.parametrize("case", _load_cases(), ids=lambda c: c["input"])
def test_parse_matches_shared_fixture(case: dict) -> None:
    """Python's parse() must reproduce the fixture it generates.

    Regression lock: if parse() changes without regenerating
    testdata/intent_cases.json, this fails here first, before it can drift
    silently out of parity with the Go classifier that also replays this
    fixture (internal/intent/intent_test.go TestParse).
    """
    got = parse(case["input"])
    assert got.intent.value == case["expected_intent"], "intent mismatch"
    assert got.text == case["expected_text"], "text mismatch"
    assert got.thought_type == case["expected_thought_type"], "thought_type mismatch"

    expected_q = case.get("expected_supersede_query")
    if expected_q is not None:
        assert got.supersede_query == expected_q
    else:
        assert got.supersede_query is None


@pytest.mark.parametrize(
    "message,expected_intent,expected_text",
    [
        ("look for tent notes", Intent.SEARCH, "tent notes"),
        ("notes about tent", Intent.SEARCH, "tent"),
        ("anything on tents", Intent.SEARCH, "tents"),
        ("remember: the tent is in the garage", Intent.CAPTURE, "the tent is in the garage"),
        ("tent notes", Intent.AMBIGUOUS, "tent notes"),
    ],
)
def test_search_phrases_and_ambiguity(message: str, expected_intent: Intent, expected_text: str) -> None:
    got = parse(message)
    assert got.intent == expected_intent, message
    assert got.text == expected_text, message


@pytest.mark.parametrize(
    "message",
    [
        "bought a new notebook today",
        "the aboutface redesign shipped",
        "relatedly the build is faster",
        "memoryless cache is fine",
    ],
)
def test_looks_ambiguous_matches_whole_words_only(message: str) -> None:
    """A substring match (e.g. 'note' inside 'notebook') must not trigger
    ambiguity: only whole-word hits on the trigger vocabulary do."""
    assert parse(message).intent == Intent.CAPTURE, message


def test_looks_ambiguous_trims_trailing_punctuation() -> None:
    assert parse("tent notes.").intent == Intent.AMBIGUOUS


def test_looks_ambiguous_requires_two_or_more_words() -> None:
    assert _looks_ambiguous("notes") is False
    assert parse("notes").intent == Intent.CAPTURE


def test_looks_ambiguous_direct() -> None:
    assert _looks_ambiguous("tent notes") is True
    assert _looks_ambiguous("memories about camping") is True
    assert _looks_ambiguous("decided to use Redis") is False
