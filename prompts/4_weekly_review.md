# Prompt Kit 4 — Weekly Review

> **Purpose:** End-of-week synthesis — clusters topics, finds connections, identifies gaps.
> Run every Friday (or your preferred review day) to close the loop on the week's thinking.

---

## Step 1 — Pull the Week's Thoughts

Call the OpenBrain MCP tool first:

```
Tool: weekly_review
Arguments: { "days": 7 }
```

This returns all thoughts from the past 7 days, grouped by type.

---

## Step 2 — The Review Prompt

Paste this into Claude along with the `weekly_review` output:

---

```
Here are my captured thoughts from the past week in OpenBrain:

[PASTE weekly_review OUTPUT HERE]

Please do a structured weekly review with the following sections:

---

### 1. Themes & Clusters
Group the thoughts into 3–5 natural themes. What were the dominant topics this week?
For each theme, list the related thoughts and give the theme a name.

### 2. Decisions Made
List every decision captured this week. For each one, note:
- Was it a reversible or irreversible decision?
- Is there any follow-up needed to validate it?

### 3. People & Relationships
List everyone mentioned this week. For each person:
- What's the current context of our relationship?
- Is there an outstanding action I should take?

### 4. Open Loops
What threads were started but not resolved? What ideas or action items were captured
but haven't had any follow-up?

### 5. Insights Worth Keeping
Which insights from this week are genuinely durable — things I'll still want to know
in 6 months? Flag these as high-value memories.

### 6. Connections Across the Week
Are there any non-obvious connections between thoughts from different contexts?
(e.g. a technical decision that relates to a conversation with a person,
or an insight that challenges a decision I made earlier)

### 7. Gaps
What important things happened this week that I *didn't* capture? Is there anything
I was thinking about a lot but there's no trace of in my brain?

### 8. Next Week Setup
Based on the open loops and patterns, suggest 3 focus areas for next week.

---

After the review, please capture the summary itself into OpenBrain using bulk_import:
- The top 3 insights as individual insight memories
- Each open loop as a note with tag 'open-loop'
- The weekly summary itself as a note with tags ['weekly-review', 'YYYY-WNN']
```

---

## Step 3 — Save the Review

After Claude generates the review, import the highlights:

```
Tool: bulk_import
Arguments:
{
  "thoughts": [
    {
      "content": "Weekly review summary for week of [date]: [summary]",
      "thought_type": "note",
      "tags": ["weekly-review", "2026-W10"],
      "summary": "Week of [date] review"
    },
    ... (open loops, top insights, etc.)
  ],
  "source": "weekly-review"
}
```

---

## Automation Option

Add to crontab to trigger a reminder every Friday at 4pm:

```cron
0 16 * * 5 notify-send "OpenBrain" "Time for your weekly review — run: pixi run weekly-review"
```

Or wire up a Telegram message via the bot that auto-runs `weekly_review` and sends you the raw data to paste into Claude.

---

## Review Cadence

| Review | Frequency | Days back | Purpose |
|--------|-----------|-----------|---------|
| Daily  | Optional  | 1         | Quick close-out |
| Weekly | Friday    | 7         | Full synthesis |
| Monthly | Last Friday | 30     | Themes and drift |
| Quarterly | Optional | 90      | Strategy and identity |
