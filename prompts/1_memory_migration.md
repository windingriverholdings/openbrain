# Prompt Kit 1 — Memory Migration

> **Purpose:** Extract everything your AI already knows about you and save it into OpenBrain.
> Run this once at setup, and again whenever you want to sync a new AI conversation history.

---

## The Prompt

Paste this into any AI assistant (Claude, ChatGPT, etc.) that has conversational history with you:

---

```
I'm setting up a personal knowledge system called OpenBrain that stores my thoughts,
decisions, and memories in a local database with semantic search.

Please review everything you know about me from our conversation history and extract
it into structured memories. For each memory, output a JSON object with this shape:

{
  "content": "<The full memory, written in first person as if I said it>",
  "thought_type": "<one of: decision | insight | person | meeting | idea | note | memory>",
  "summary": "<one sentence summary>",
  "tags": ["<tag1>", "<tag2>"],
  "metadata": {
    "<any relevant key>": "<value>"
  }
}

Categories to extract:
1. **Decisions** — choices I've made (technical, personal, professional)
2. **People** — individuals I've mentioned, their role, relationship to me
3. **Projects** — things I'm working on, their status, key details
4. **Preferences** — how I like to work, tools I use, opinions I hold
5. **Insights** — things I've learned or realised
6. **Meetings** — conversations, calls, events I've mentioned
7. **Ideas** — things I want to explore or build

Output a JSON array of all extracted memories. Be thorough — include anything
that would help a future AI assistant understand who I am and how I work.
```

---

## After Running the Prompt

Take the JSON output and call the OpenBrain MCP tool:

```
Tool: bulk_import
Arguments:
{
  "thoughts": <paste the JSON array here>,
  "source": "migration"
}
```

---

## Tips

- Run this against Claude, ChatGPT, and any other AI tools you use regularly
- The more context each AI has, the more memories it will surface
- Re-run monthly to capture new learnings
- After import, use `search_thoughts` to verify the memories are retrievable
