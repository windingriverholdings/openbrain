# OPENBRAIN.md — Your Long-Term Semantic Memory

OpenBrain is your persistent, searchable knowledge base. It stores thoughts, decisions,
insights, and memories as vector embeddings in a local PostgreSQL database.
**No cloud. No external dependency. Fully private.**

Call it via `mcporter call openbrain.<tool> [args]`.

---

## When to USE OpenBrain

**Search before answering** — if Craig asks about something he might have thought about before,
search OpenBrain first. Don't rely only on this session's context.

```bash
mcporter call openbrain.search_thoughts query="your natural language question" top_k=5
```

**Capture high-value content** — after any conversation where a real decision, insight,
or important person/meeting comes up, save it:

```bash
mcporter call openbrain.capture_thought \
  content="We decided to use fastembed over Ollama because it runs in-process" \
  thought_type="decision" \
  tags='["openbrain","infrastructure"]' \
  source="telegram"
```

**Weekly review** — when Craig asks for a weekly review or summary:

```bash
mcporter call openbrain.weekly_review days=7
```

**Stats** — when asked how much is in the brain:

```bash
mcporter call openbrain.brain_stats
```

**Bulk import** — after a memory migration or batch of notes:

```bash
mcporter call openbrain.bulk_import thoughts='[{"content":"...","thought_type":"memory"}]' source="import"
```

---

## Thought Types

| Type | When to use |
|------|-------------|
| `decision` | A choice made — technical, personal, strategic |
| `insight` | A realisation or lesson learned |
| `person` | Someone Craig mentioned — name, role, context |
| `meeting` | A call, conversation, or event |
| `idea` | Something to explore or build |
| `note` | General capture that doesn't fit above |
| `memory` | Historical fact about Craig's life/work |

---

## Signal vs Noise — The Filter Rule

**DO capture:**
- Explicit decisions ("decided to...", "chose X over Y")
- Named insights ("realised that...", "key learning:")
- People with context ("met [name] who...")
- Commitments and action items
- Project status changes
- Opinions Craig expresses strongly

**DO NOT capture:**
- Conversational back-and-forth
- Questions Craig asks (unless the answer is the insight)
- Chitchat, jokes, casual chat
- Anything Craig prefixes with "just curious" or "random thought"

---

## Relationship to MEMORY.md

- **MEMORY.md** = your working session memory (flat markdown, fast to read)
- **OpenBrain** = long-term semantic memory (searchable by meaning, not just text)

When you update MEMORY.md with something significant, also consider saving it
to OpenBrain for future semantic retrieval.

When Craig asks a question that might have a prior answer, search OpenBrain first,
then check MEMORY.md.
