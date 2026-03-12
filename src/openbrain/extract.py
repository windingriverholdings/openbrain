"""LLM-assisted extraction — turns long-form text into structured thought candidates.

Uses the configured LLM provider (Ollama/Claude) to parse meeting notes,
conversations, or long captures into individual typed thoughts.
"""

from __future__ import annotations

import json
from typing import Any

import structlog

from .llm import get_provider

logger = structlog.get_logger(__name__)

EXTRACTION_SYSTEM = (
    "You are a knowledge extraction assistant for a personal knowledge base. "
    "Extract distinct, standalone thoughts from the input text. "
    "Return ONLY a valid JSON array — no markdown fences, no commentary."
)

EXTRACTION_PROMPT = """\
Analyze this text and extract distinct thoughts. For each thought, provide:
- content: the core information (1-3 sentences, standalone — someone reading it \
without context should understand it)
- thought_type: one of decision, insight, person, meeting, idea, note, memory
- tags: relevant tags as a list of lowercase strings
- subjects: people, tools, places, or concepts this thought is about \
(list of strings)
- supersedes_query: if this updates a previous fact, a search query to find \
the old thought (null otherwise)

Text to analyze:
{input_text}

Return a JSON array of objects. Example format:
[
  {{
    "content": "Decided to switch from Redis to Valkey for session caching.",
    "thought_type": "decision",
    "tags": ["caching", "infrastructure"],
    "subjects": ["Valkey", "Redis"],
    "supersedes_query": "Redis caching decision"
  }}
]
"""

VALID_THOUGHT_TYPES = frozenset(
    ["decision", "insight", "person", "meeting", "idea", "note", "memory"]
)


def _parse_extraction_response(raw: str) -> list[dict[str, Any]]:
    """Parse LLM output into a list of thought candidates, handling common issues."""
    text = raw.strip()

    # Strip markdown code fences if present
    if text.startswith("```"):
        lines = text.split("\n")
        lines = [ln for ln in lines if not ln.strip().startswith("```")]
        text = "\n".join(lines).strip()

    try:
        parsed = json.loads(text)
    except json.JSONDecodeError:
        # Try to find a JSON array in the response
        start = text.find("[")
        end = text.rfind("]")
        if start != -1 and end != -1:
            try:
                parsed = json.loads(text[start:end + 1])
            except json.JSONDecodeError:
                logger.warning("extraction_json_parse_failed", raw_len=len(raw))
                return []
        else:
            logger.warning("extraction_no_json_found", raw_len=len(raw))
            return []

    if not isinstance(parsed, list):
        parsed = [parsed]

    candidates = []
    for item in parsed:
        if not isinstance(item, dict):
            continue
        content = item.get("content", "").strip()
        if not content:
            continue

        thought_type = item.get("thought_type", "note")
        if thought_type not in VALID_THOUGHT_TYPES:
            thought_type = "note"

        tags = item.get("tags", [])
        if not isinstance(tags, list):
            tags = []
        tags = [str(t).lower().strip() for t in tags if t]

        subjects = item.get("subjects", [])
        if not isinstance(subjects, list):
            subjects = []
        subjects = [str(s).strip() for s in subjects if s]

        supersedes_query = item.get("supersedes_query")
        if supersedes_query and not isinstance(supersedes_query, str):
            supersedes_query = None

        candidates.append({
            "content": content,
            "thought_type": thought_type,
            "tags": tags,
            "subjects": subjects,
            "supersedes_query": supersedes_query,
        })

    return candidates


async def extract_thoughts(text: str) -> list[dict[str, Any]]:
    """Extract structured thought candidates from long-form text using LLM."""
    provider = get_provider(text)
    if provider is None:
        return []

    prompt = EXTRACTION_PROMPT.format(input_text=text)

    logger.info("extraction_starting", text_len=len(text))
    raw_response = await provider.generate(prompt, system=EXTRACTION_SYSTEM)
    logger.info("extraction_raw_response", response_len=len(raw_response))

    candidates = _parse_extraction_response(raw_response)
    logger.info("extraction_complete", candidates=len(candidates))

    return candidates
