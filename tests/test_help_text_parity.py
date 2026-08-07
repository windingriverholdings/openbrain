"""Parity test: src/openbrain/intent.py's HELP_TEXT must match
internal/intent/help.go's HelpText exactly.

Both are independent copies of the same user-facing help message for two
different runtimes (the legacy Python CLI/MCP path and the Go rewrite). A
prior "sync" (OB-079) claimed parity by eyeballing a diff and missed a real
divergence ("update: Alice moved to team B" vs "update: Alice moved to the
platform team", inherited from an earlier genericization commit that touched
both files independently). Per prove-or-mark, an unproven parity claim is not
proof: this test enforces the invariant mechanically instead of by hand.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent
_HELP_GO = _REPO_ROOT / "internal" / "intent" / "help.go"

sys.path.insert(0, str(_REPO_ROOT / "src"))
from openbrain.intent import HELP_TEXT  # noqa: E402


def _extract_go_help_text() -> str:
    """Pull the raw-string body of `const HelpText = \\`...\\`` out of help.go.

    HelpText is a Go raw string literal (backtick-delimited) with no escape
    sequences and no backtick characters in its content, so this is a plain
    substring extraction between the first backtick after the `const
    HelpText =` declaration and the file's closing backtick: not a general
    Go-string parser, and not meant to be one. If help.go ever needs a
    backtick inside HelpText's content, this extraction breaks loudly (the
    regex simply fails to match) rather than silently mis-parsing, so it
    fails closed.
    """
    source = _HELP_GO.read_text(encoding="utf-8")
    match = re.search(r"const HelpText = `(.*)`", source, re.DOTALL)
    assert match is not None, (
        "could not find `const HelpText = `...`` in internal/intent/help.go; "
        "the parity check's extraction assumes a single unescaped backtick-"
        "delimited raw string with no backtick in its content"
    )
    return match.group(1)


class TestHelpTextParity:
    def test_python_help_text_matches_go_help_text_exactly(self) -> None:
        go_text = _extract_go_help_text()
        assert HELP_TEXT == go_text, (
            "src/openbrain/intent.py's HELP_TEXT has drifted from "
            "internal/intent/help.go's HelpText. Edit whichever one is "
            "stale so both read identically; they are two independent "
            "copies of the same user-facing text with nothing enforcing "
            "sync except this test."
        )
