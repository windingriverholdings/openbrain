"""Simple intent detection for natural language input.

Classifies a message into an OpenBrain action without requiring an LLM.
Covers the common patterns; anything unrecognised falls back to capture.

# TODO(tailscale): If exposing over the network, add rate limiting here.
# TODO(llm): Replace regex intent matching with a local LLM classifier
#             (e.g. Ollama + llama3) for richer natural language understanding.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from enum import Enum


class Intent(Enum):
    CAPTURE = "capture"
    SEARCH = "search"
    REVIEW = "review"
    STATS = "stats"
    HELP = "help"


@dataclass
class ParsedIntent:
    intent: Intent
    text: str                        # cleaned content/query
    thought_type: str = "note"
    tags: list[str] | None = None


_SEARCH_PATTERNS = re.compile(
    r"^(search|find|look up|what do i know about|recall|remember|remind me|"
    r"have i (thought|noted|written) about|show me|retrieve|query)[:\s]+",
    re.IGNORECASE,
)

_CAPTURE_PATTERNS = re.compile(
    r"^(remember|save|capture|note|log|store|record|add|write down|"
    r"decided?|insight:|learning:|realised?|met |meeting with)[:\s]*",
    re.IGNORECASE,
)

_DECISION_HINT = re.compile(
    r"\b(decided?|chose|choice|picked|going with|will use|won't use|rejected)\b",
    re.IGNORECASE,
)

_INSIGHT_HINT = re.compile(
    r"\b(realised?|learned?|noticed|insight|pattern|key (takeaway|learning))\b",
    re.IGNORECASE,
)

_PERSON_HINT = re.compile(
    r"\b(met |talked? (to|with)|called?|spoke (to|with)|email(ed)?)\b",
    re.IGNORECASE,
)

_MEETING_HINT = re.compile(
    r"\b(meeting|standup|call|sync|retrospective|1:1|one.on.one)\b",
    re.IGNORECASE,
)

_REVIEW_PATTERNS = re.compile(
    r"^(weekly review|week review|review( the)? week|what happened this week"
    r"|this week|past week|last 7 days|summarise( the)? week)",
    re.IGNORECASE,
)

_STATS_PATTERNS = re.compile(
    r"^(stats|statistics|how many (thoughts|memories|notes)|"
    r"brain stats|knowledge base stats|count)",
    re.IGNORECASE,
)

_HELP_PATTERNS = re.compile(
    r"^(help|commands|what can you do|\?+|how (do|does) (this|it) work)",
    re.IGNORECASE,
)


def _infer_type(text: str) -> str:
    if _DECISION_HINT.search(text):
        return "decision"
    if _INSIGHT_HINT.search(text):
        return "insight"
    if _PERSON_HINT.search(text):
        return "person"
    if _MEETING_HINT.search(text):
        return "meeting"
    return "note"


def parse(message: str) -> ParsedIntent:
    """Parse a natural language message into a structured intent."""
    msg = message.strip()

    if _HELP_PATTERNS.match(msg):
        return ParsedIntent(intent=Intent.HELP, text=msg)

    if _STATS_PATTERNS.match(msg):
        return ParsedIntent(intent=Intent.STATS, text=msg)

    if _REVIEW_PATTERNS.match(msg):
        return ParsedIntent(intent=Intent.REVIEW, text=msg)

    search_match = _SEARCH_PATTERNS.match(msg)
    if search_match:
        query = msg[search_match.end():].strip()
        return ParsedIntent(intent=Intent.SEARCH, text=query)

    capture_match = _CAPTURE_PATTERNS.match(msg)
    if capture_match:
        content = msg[capture_match.end():].strip() or msg
        return ParsedIntent(
            intent=Intent.CAPTURE,
            text=content,
            thought_type=_infer_type(content),
        )

    # Default: try to capture anything that looks like a statement
    # Short questions fall back to search
    if msg.endswith("?") or msg.lower().startswith(("what", "who", "when", "where", "how", "why")):
        return ParsedIntent(intent=Intent.SEARCH, text=msg.rstrip("?"))

    return ParsedIntent(
        intent=Intent.CAPTURE,
        text=msg,
        thought_type=_infer_type(msg),
    )


HELP_TEXT = """
**OpenBrain** — your personal knowledge base

**Capture a thought:**
> decided to use Redis for session caching
> realised that deploys on Fridays are always risky
> met Sarah Chen, she runs engineering at Acme
> remember: the API rate limit is 1000 req/min

**Search:**
> search: Redis decisions
> what do I know about Sarah?
> find: deployment lessons

**Weekly review:**
> weekly review
> what happened this week?

**Stats:**
> stats
> how many thoughts?

Anything that looks like a statement gets captured automatically.
Anything that looks like a question triggers a search.
""".strip()
