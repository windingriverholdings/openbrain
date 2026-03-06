# Prompt Kit 2 — Open Brain Spark

> **Purpose:** An interview prompt that discovers how OpenBrain fits your specific workflow.
> Run this once at setup to personalise your capture habits and retrieval patterns.

---

## The Prompt

Paste this into Claude (or your preferred AI):

---

```
I've just set up OpenBrain — a personal knowledge system that lets me capture thoughts
from any interface (Telegram, Claude, web) and retrieve them later using semantic search.

I want you to interview me to understand how I think and work, so we can configure
OpenBrain to fit my natural workflow. Ask me one question at a time and wait for my
answer before continuing.

Interview areas to cover:

1. **Capture habits** — When do I naturally want to record things? (In the moment?
   After a meeting? Before bed?) What do I lose track of most often?

2. **Thought types** — Which of these matter most to me: decisions, insights, people
   I meet, meeting notes, ideas, or long-term memories?

3. **Retrieval patterns** — How do I typically look things back up? By topic? By person?
   By time period? Do I browse or search with a specific question?

4. **Interfaces** — Which channels do I want to capture from? (Telegram bot, Claude
   conversation, web UI, CLI, all of the above?)

5. **Tags and structure** — Do I prefer to tag things manually, or should the AI
   suggest tags automatically based on content?

6. **Weekly rhythm** — Would I want an automated weekly review? What day and format
   would work best?

7. **Privacy** — Are there categories of thought I never want stored, or that should
   be kept under a specific tag for easy exclusion from searches?

After the interview, summarise my answers as:
- A recommended default `thought_type` for my most common captures
- A starter set of tags that fit my workflow
- A suggested weekly review schedule
- Any OpenBrain configuration changes I should make based on my answers

Let's start with question 1.
```

---

## What to Do with the Output

After the interview, update your `.env` with any recommended defaults:

```bash
# Example output from interview:
OPENBRAIN_SEARCH_TOP_K=15          # if you prefer more results
OPENBRAIN_SEARCH_SCORE_THRESHOLD=0.30  # lower = broader recall
```

Save the interview summary itself as an OpenBrain memory:

```
Tool: capture_thought
{
  "content": "<paste your workflow summary>",
  "thought_type": "insight",
  "tags": ["openbrain", "workflow", "setup"],
  "summary": "My OpenBrain workflow preferences from initial setup interview"
}
```
