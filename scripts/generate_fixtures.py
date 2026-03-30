#!/usr/bin/env python3
"""Generate behavioral test fixtures from the Python implementation.

Runs intent.parse(), extract._parse_extraction_response(), and
llm._needs_primary_model() against comprehensive inputs, dumping
results to testdata/*.json for Go test consumption.
"""

import json
import sys
from pathlib import Path

# Add src to path
sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "src"))

from openbrain.intent import parse, _infer_type, DEEP_CAPTURE_THRESHOLD
from openbrain.extract import _parse_extraction_response, VALID_THOUGHT_TYPES
from openbrain.llm import _needs_primary_model

TESTDATA = Path(__file__).resolve().parent.parent / "testdata"


def generate_intent_fixtures():
    """Generate intent classification test cases."""
    inputs = [
        # === HELP ===
        "help",
        "Help",
        "HELP",
        "commands",
        "what can you do",
        "What can you do?",
        "?",
        "???",
        "how does this work",
        "how do this work",

        # === STATS ===
        "stats",
        "stat",
        "statistics",
        "how many thoughts",
        "how many memories",
        "how many notes",
        "brain stats",
        "knowledge base stats",
        "count",
        "send me some stats",
        "give me the status",
        "show me my stats",
        "what are my stats",
        "what is the status",

        # === REVIEW ===
        "weekly review",
        "week review",
        "review the week",
        "review week",
        "what happened this week",
        "this week",
        "past week",
        "last 7 days",
        "summarise the week",
        "summarise week",
        "give me a weekly review",
        "give me a review",
        "show me the weekly review",
        "show me the review",

        # === EXTRACT (prefix) ===
        "extract: meeting notes from today's standup",
        "Extract: here are my notes from the call",
        "EXTRACT: long form text goes here",

        # === SUPERSEDE ===
        "actually, we switched from Redis to Valkey",
        "Actually we now use PostgreSQL",
        "update: Sarah moved to booth 7",
        "correction: the rate limit is 2000, not 1000",
        "changed: deploy schedule is now weekly",
        "no longer using Docker for local dev",
        "now instead, we use Kubernetes",

        # === SEARCH (explicit) ===
        "search: Redis decisions",
        "find: deployment lessons",
        "look up: project deadlines",
        "what do I know about Sarah",
        "recall: meeting with Alice",
        "remember: the API rate limit",
        "remind me: what was the password policy",
        "have I thought about caching",
        "have I noted about Docker",
        "show me: infrastructure decisions",
        "retrieve: last month's insights",
        "query: Go vs Rust",

        # === CAPTURE (explicit) ===
        "remember: the API rate limit is 1000 req/min",
        "save: need to update the docs",
        "capture: deployment took 45 minutes today",
        "note: review PR #123 tomorrow",
        "log: server restarted at 3am",
        "store: backup cron runs at midnight",
        "record: meeting scheduled for Thursday",
        "add: new team member joining Monday",
        "write down: the wifi password is hunter2",
        "decided to use Go for the rewrite",
        "decided: we will migrate to Go",
        "insight: tests run faster with parallel execution",
        "learning: pgvector HNSW is faster than IVFFlat",
        "realised that Friday deploys are risky",
        "realized the build takes too long",
        "met Sarah Chen, she runs engineering at Acme",
        "meeting with the platform team about auth",

        # === SEARCH (implicit — questions) ===
        "what did I decide about caching?",
        "who is Sarah Chen?",
        "when was the last deploy?",
        "where is the config file?",
        "how does the auth flow work?",
        "why did we choose PostgreSQL?",
        "what are our deployment practices?",

        # === EXTRACT (implicit — long text >200 chars) ===
        "Today we had a productive meeting with the platform team. We discussed the migration from Python to Go. Key decisions: 1) Use pgx for database access, 2) Use mcp-go for the MCP server, 3) Start with Ollama for embeddings. Sarah will handle the CI/CD pipeline. Next meeting is Thursday.",

        # === CAPTURE (implicit — short statements) ===
        "the server is running on port 8080",
        "PostgreSQL version is 16.2",
        "I like Go better than Python for this",
        "deploy window is 2am-4am PST",

        # === Edge cases ===
        "",
        "   ",
        "a",
        "Hello",
        "This is a medium-length statement that doesn't match any explicit patterns but is under 200 characters so it should default to capture.",
    ]

    fixtures = []
    for inp in inputs:
        result = parse(inp)
        fixture = {
            "input": inp,
            "expected_intent": result.intent.value,
            "expected_text": result.text,
            "expected_thought_type": result.thought_type,
        }
        if result.tags is not None:
            fixture["expected_tags"] = result.tags
        if result.supersede_query is not None:
            fixture["expected_supersede_query"] = result.supersede_query
        fixtures.append(fixture)

    return fixtures


def generate_infer_type_fixtures():
    """Generate thought type inference test cases."""
    inputs = [
        ("decided to use Redis for caching", "decision"),
        ("chose Go over Rust", "decision"),
        ("going with PostgreSQL for the database", "decision"),
        ("will use Docker for deployment", "decision"),
        ("won't use MongoDB anymore", "decision"),
        ("rejected the microservices approach", "decision"),
        ("picked the blue theme", "decision"),
        ("realised Friday deploys are risky", "insight"),
        ("realized the tests were flaky", "insight"),
        ("learned that pgvector needs HNSW", "insight"),
        ("noticed a pattern in the logs", "insight"),
        ("insight: parallel tests are faster", "insight"),
        ("key takeaway from the retro", "insight"),
        ("key learning: always test locally first", "insight"),
        ("met Sarah Chen at the conference", "person"),
        ("talked to Bob about the migration", "person"),
        ("talked with Alice about auth", "person"),
        ("called the vendor about pricing", "person"),
        ("spoke to the CTO about roadmap", "person"),
        ("spoke with engineering about timelines", "person"),
        ("emailed the team about the outage", "person"),
        ("meeting with platform team", "meeting"),
        ("standup was quick today", "meeting"),
        ("sync with frontend team", "meeting"),
        ("retrospective went well", "meeting"),
        ("1:1 with my manager", "meeting"),
        ("one on one with Sarah", "meeting"),
        ("the server runs on port 8080", "note"),
        ("pizza is great", "note"),
        ("need to update the docs", "note"),
        ("", "note"),
    ]

    fixtures = []
    for text, expected in inputs:
        result = _infer_type(text)
        fixtures.append({
            "input": text,
            "expected_type": expected,
            "actual_type": result,
        })
        if result != expected:
            print(f"  WARNING: _infer_type mismatch for '{text}': expected={expected}, got={result}")

    return fixtures


def generate_extract_parse_fixtures():
    """Generate extraction response parsing test cases."""
    cases = [
        {
            "name": "valid_json_array",
            "raw": '[{"content":"Decided to use Go","thought_type":"decision","tags":["go","rewrite"]}]',
        },
        {
            "name": "markdown_fenced_json",
            "raw": '```json\n[{"content":"Test thought","thought_type":"note","tags":[]}]\n```',
        },
        {
            "name": "markdown_fenced_no_lang",
            "raw": '```\n[{"content":"Another thought","thought_type":"insight","tags":["testing"]}]\n```',
        },
        {
            "name": "embedded_json_with_preamble",
            "raw": 'Here are the extracted thoughts:\n[{"content":"Embedded thought","thought_type":"idea","tags":["embedded"]}]\nHope this helps!',
        },
        {
            "name": "single_object_not_array",
            "raw": '{"content":"Single thought","thought_type":"decision","tags":["solo"]}',
        },
        {
            "name": "invalid_json",
            "raw": "this is not json at all",
        },
        {
            "name": "empty_array",
            "raw": "[]",
        },
        {
            "name": "empty_content_filtered",
            "raw": '[{"content":"","thought_type":"note","tags":[]},{"content":"Valid","thought_type":"note","tags":[]}]',
        },
        {
            "name": "invalid_thought_type_defaults_to_note",
            "raw": '[{"content":"Has bad type","thought_type":"banana","tags":[]}]',
        },
        {
            "name": "non_dict_items_filtered",
            "raw": '[{"content":"Good item","thought_type":"note","tags":[]}, "bad string item", 42]',
        },
        {
            "name": "tags_normalization",
            "raw": '[{"content":"Tag test","thought_type":"note","tags":["  Go  ","RUST","  python "]}]',
        },
        {
            "name": "tags_not_a_list",
            "raw": '[{"content":"Bad tags","thought_type":"note","tags":"not-a-list"}]',
        },
        {
            "name": "subjects_extraction",
            "raw": '[{"content":"About people","thought_type":"person","tags":[],"subjects":["Alice","Bob"]}]',
        },
        {
            "name": "subjects_not_a_list",
            "raw": '[{"content":"Bad subjects","thought_type":"note","tags":[],"subjects":"not-a-list"}]',
        },
        {
            "name": "supersedes_query_string",
            "raw": '[{"content":"Updated fact","thought_type":"decision","tags":[],"supersedes_query":"old Redis decision"}]',
        },
        {
            "name": "supersedes_query_not_string",
            "raw": '[{"content":"Bad supersede","thought_type":"note","tags":[],"supersedes_query":42}]',
        },
        {
            "name": "multiple_thoughts",
            "raw": json.dumps([
                {"content": "First thought", "thought_type": "decision", "tags": ["a"]},
                {"content": "Second thought", "thought_type": "insight", "tags": ["b"]},
                {"content": "Third thought", "thought_type": "person", "tags": ["c"], "subjects": ["Alice"]},
            ]),
        },
        {
            "name": "whitespace_content_filtered",
            "raw": '[{"content":"   ","thought_type":"note","tags":[]}]',
        },
        {
            "name": "missing_content_key",
            "raw": '[{"thought_type":"note","tags":[]}]',
        },
    ]

    fixtures = []
    for case in cases:
        result = _parse_extraction_response(case["raw"])
        fixtures.append({
            "name": case["name"],
            "raw_input": case["raw"],
            "expected_count": len(result),
            "expected_results": result,
        })

    return fixtures


def generate_llm_routing_fixtures():
    """Generate LLM routing decision test cases."""
    threshold = 500  # default from config

    cases = [
        # Short, no correction patterns -> fast
        ("short simple text", threshold, False),
        ("decided to use Go", threshold, False),
        ("meeting notes from today", threshold, False),

        # Correction patterns -> primary
        ("actually, we switched to Valkey", threshold, True),
        ("correction: the rate limit is 2000", threshold, True),
        ("update: Sarah moved to booth 7", threshold, True),
        ("no longer using Docker", threshold, True),
        ("now instead, we use Kubernetes", threshold, True),
        ("changed: deploy schedule is weekly", threshold, True),
        ("not 50% but 80% of the team agreed", threshold, True),
        ("was wrong about the deadline", threshold, True),
        ("I was mistaken about the config", threshold, True),
        ("turns out the issue was DNS", threshold, True),

        # Long text -> primary (regardless of content)
        ("x" * 501, threshold, True),
        ("x" * 1000, threshold, True),

        # At threshold boundary
        ("x" * 500, threshold, False),
        ("x" * 499, threshold, False),

        # Empty text
        ("", threshold, False),
    ]

    fixtures = []
    for text, thresh, expected in cases:
        result = _needs_primary_model(text, thresh)
        fixtures.append({
            "input_text": text if len(text) <= 100 else f"[{len(text)} chars of '{text[:20]}...']",
            "input_text_len": len(text),
            "threshold": thresh,
            "expected_needs_primary": expected,
            "actual_needs_primary": result,
        })
        if result != expected:
            print(f"  WARNING: _needs_primary_model mismatch for '{text[:50]}': expected={expected}, got={result}")

    return fixtures


def main():
    print("Generating intent fixtures...")
    intent_fixtures = generate_intent_fixtures()
    (TESTDATA / "intent_cases.json").write_text(json.dumps(intent_fixtures, indent=2) + "\n")
    print(f"  -> {len(intent_fixtures)} cases written to testdata/intent_cases.json")

    print("Generating infer_type fixtures...")
    type_fixtures = generate_infer_type_fixtures()
    (TESTDATA / "infer_type_cases.json").write_text(json.dumps(type_fixtures, indent=2) + "\n")
    print(f"  -> {len(type_fixtures)} cases written to testdata/infer_type_cases.json")

    print("Generating extract parse fixtures...")
    extract_fixtures = generate_extract_parse_fixtures()
    (TESTDATA / "extract_parse_cases.json").write_text(json.dumps(extract_fixtures, indent=2) + "\n")
    print(f"  -> {len(extract_fixtures)} cases written to testdata/extract_parse_cases.json")

    print("Generating LLM routing fixtures...")
    llm_fixtures = generate_llm_routing_fixtures()
    (TESTDATA / "llm_routing_cases.json").write_text(json.dumps(llm_fixtures, indent=2) + "\n")
    print(f"  -> {len(llm_fixtures)} cases written to testdata/llm_routing_cases.json")

    print("\nDone! All fixtures written to testdata/")


if __name__ == "__main__":
    main()
