package intent

// HelpText is the user-facing help message.
const HelpText = `**OpenBrain** — your personal knowledge base

**Capture a thought:**
> decided to use Redis for session caching
> realised that deploys on Fridays are always risky
> met Sarah Chen, she runs engineering at Acme
> remember: the API rate limit is 1000 req/min

**Update a fact (supersede):**
> actually, we switched from Redis to Valkey
> update: Sarah moved to booth 7
> correction: the rate limit is 2000, not 1000

**Deep capture (extract multiple thoughts):**
> extract: [paste meeting notes or long text]
> (or just paste 200+ characters — auto-detected)

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

**Admin:**
> /reload — reload config from .env (no restart needed)

Anything that looks like a statement gets captured automatically.
Anything that looks like a question triggers a search.`
