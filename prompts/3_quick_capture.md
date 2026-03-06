# Prompt Kit 3 — Quick Capture Templates

> **Purpose:** Sentence starters optimised for clean metadata extraction.
> These patterns produce well-structured thoughts that embed and retrieve reliably.
> Use them via Telegram, Claude, or any interface connected to OpenBrain.

---

## How to Use

Start your message with one of the sentence starters below. OpenBrain will automatically
classify the `thought_type` from the prefix and extract clean metadata.

The AI layer (Claude via MCP) will parse the sentence and call `capture_thought`
with the correct fields populated.

---

## Decision Captures

```
Decided to [action] because [reason].
I chose [option A] over [option B] because [rationale].
We agreed that [decision] after [context].
```

**Examples:**
```
Decided to use fastembed over Ollama because it runs in-process and avoids HTTP overhead.
I chose PostgreSQL over SQLite because we need concurrent multi-interface access.
```

---

## Insight Captures

```
Realised that [insight].
Key learning: [what you learned] from [context].
Pattern I noticed: [observation].
```

**Examples:**
```
Realised that my best ideas come during walks, not at my desk.
Key learning: weekly reviews are only useful if done on Friday, not Monday.
Pattern I noticed: I always under-estimate tasks involving third-party APIs.
```

---

## Person Captures

```
Met [name] who [role/context]. They [key detail].
[Name] mentioned that [important thing].
Contact: [name], [role at company], [how we connected].
```

**Examples:**
```
Met Sarah Chen who runs engineering at Acme. She's working on a distributed cache problem.
James mentioned that his team is evaluating our API for their internal tooling.
```

---

## Meeting Captures

```
Meeting with [who] about [topic]. [Key outcome or action].
Call with [name/team]: [summary]. Action: [next step].
[Event]: [what happened]. [Decision or takeaway].
```

**Examples:**
```
Meeting with the ops team about deployment pipeline. Agreed to move to GitHub Actions.
Call with client: they want a prototype by end of month. Action: scope it by Wednesday.
```

---

## Idea Captures

```
Idea: [concept]. Might work because [reason].
What if [hypothesis]?
Future project: [description].
```

**Examples:**
```
Idea: use TimescaleDB's continuous aggregates for the weekly review query. Might work because it pre-computes the groupings.
What if we exposed OpenBrain's search as a Telegram inline query?
```

---

## Note / General Captures

```
Note: [anything].
Remember: [thing to recall later].
Context: [background information].
```

---

## Batch Capture (for after meetings)

Paste this into Claude after a meeting:

```
I just finished [type of meeting] with [who] about [topic].

Here's what happened:
[your raw notes or brain dump]

Please extract this into OpenBrain memories using the capture_thought tool.
Create separate memories for:
- Any decisions made
- Any people mentioned with their context
- Any action items or commitments (tag as 'action-item')
- Any insights or learnings
- The meeting itself as a summary
```
