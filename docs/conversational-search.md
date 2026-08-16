# Conversational Search

This document describes the conversational search mode from the user's message
through local-model generation, retrieval, citation verification, and the web
client response.

## Purpose

Ordinary search returns ranked notes. Conversational search uses those notes as
grounding context and asks a local Ollama model to answer the user's question
in natural language.

The answer is constrained by this rule:

> Use only the supplied OpenBrain notes. Do not invent facts. Say when the
> notes are insufficient and mention conflicts.

The implementation is intentionally designed for small local models. It does
not ask a model to plan an unbounded research process. Instead, it uses two
bounded model calls and deterministic retrieval in between:

```text
user question
    |
    |  local model call 1: rewrite into a retrieval query
    v
one embedding + three retrieval axes
    |
    |  deterministic reciprocal-rank fusion and score-gap cutoff
    v
up to eight grounding notes, within an aggregate prompt budget
    |
    |  local model call 2: stream an answer with [n] citations
    v
citation verification + WebSocket response
```

## 1. User Input

The web client has two separate choices:

- **Semantic** search returns ranked search results.
- **Conversational** search returns a grounded answer and source notes.

The strategy buttons and their tooltips are generated in
`cmd/openbrain-web/static/composer.js`. The conversational strategy sends a
WebSocket message with `mode: "conversational"` and `intent: "search"`.

The WebSocket handler in `cmd/openbrain-web/handler.go` recognizes that mode
and calls:

```go
b.ConverseSearch(ctx, parsed.Text, searchOpts, onChunk, onStatus)
```

The browser receives status messages such as:

- `Thinking...`
- `Understanding what to look for...`
- `Searching your brain for "..."...`
- `Reading 8 relevant notes...`
- `Writing a grounded answer...`

The initial `Thinking...` message opens the streaming assistant bubble before
the first model request finishes.

## 2. Request Deadline and Model Setup

`Brain.ConverseSearch` applies `OPENBRAIN_SEARCH_AGENT_TIMEOUT` as an outer
backstop. The Ollama provider applies separate per-call deadlines:

- `OPENBRAIN_CONVERSE_REWRITE_TIMEOUT`, default `25s`
- `OPENBRAIN_CONVERSE_ANSWER_TIMEOUT`, default `90s`

This is important because one slow operation must not consume the entire
budget for the next operation.

At startup, `brain.New` creates the conversational provider and performs an
Ollama `/api/show` preflight for the configured model. If the model is missing,
the log names the model and tells the operator to pull it or fix
`OPENBRAIN_CONVERSE_MODEL`.

The current local configuration uses:

```env
OPENBRAIN_CONVERSE_MODEL=qwen3.5:4b-mlx
OPENBRAIN_EMBEDDING_MODEL=qwen3-embedding:0.6b
```

The model choice is intentional. A large generation model and the embedding
model cannot always remain resident together on a 16 GB machine. Repeatedly
switching between embedding and generation can evict and reload the model,
which was a major contributor to the previous `context deadline exceeded`
error.

The provider sends these Ollama settings on every generation request:

- `options.num_ctx`: explicit context window, default `8192`
- `options.num_predict`: bounded output length
- `options.temperature`: low temperature, currently `0.2`
- `keep_alive`: default `30m`, keeping the generation model resident between
  the rewrite and answer calls
- `think: false`, preventing hidden reasoning tokens from consuming the local
  model's latency budget

The provider also uses a dedicated HTTP client, raises the streaming scanner
limit above the default 64 KB, and includes the model, step, prompt estimate,
and timeout in diagnostic errors.

## 3. Query Rewriting

The first model call is `UnderstandQuery` in
`internal/converse/converse.go`.

The model is instructed to return only a short retrieval query. It should keep
names, acronyms, subjects, and important concepts, while removing conversational
filler such as `what is` or `can you tell me about`.

Example:

```text
Question: What did I decide about the search timeout?
Query: search timeout decision
```

The response is sanitized to remove quotes, code fences, labels such as
`Query:`, and explanations after the first line. A rewrite longer than 240
runes is rejected as answer-shaped output.

A rewrite failure is recoverable. The search uses the original user question
instead of returning an error. Cancellation is the exception and propagates
normally.

## 4. Deterministic Retrieval

The retrieval implementation is `converseRetrieve` in
`internal/brain/brain.go`.

### 4.1 One embedding

The rewritten query is embedded exactly once through the configured Ollama
embedding model. The same vector is reused for the hybrid retrieval axis.
The old implementation embedded the same query once per round, which wasted
time and caused more model eviction opportunities.

### 4.2 Three retrieval axes

The system over-fetches candidates, normally three times the configured maximum
note count, then runs three axes:

1. **Hybrid search on the rewritten query**: combines keyword and semantic
   matching in PostgreSQL/pgvector.
2. **Keyword search on the rewritten query**: preserves exact terms from the
   model's retrieval focus.
3. **Keyword search on the original question**: protects against a poor rewrite
   dropping a name, acronym, or rare phrase.

The raw scores from keyword search and vector search are not directly
comparable. Keyword scores are relatively small while cosine-based semantic
scores are larger. Therefore the three result lists are combined with
reciprocal-rank fusion through `internal/rankfuse` rather than by adding raw
scores.

### 4.3 Result cutoff

The fused list is trimmed by two bounds:

- `OPENBRAIN_CONVERSE_MAX_NOTES`, default `8`
- A relative score-gap cutoff, while preserving a minimum of two notes when
  two notes exist

This prevents a narrow question from receiving a full prompt of irrelevant
notes while still allowing broad questions to use more context.

The default is one retrieval round. `OPENBRAIN_CONVERSE_MAX_ROUNDS` can allow
additional rounds, but only then is the optional model sufficiency gate
consulted. The normal path does not call that gate.

## 5. Prompt Budgeting

The selected notes are formatted by `FormatNotes` in
`internal/converse/notes.go`.

The important constraint is an **aggregate** budget, not just a maximum per
note. The default is:

```env
OPENBRAIN_CONVERSE_PROMPT_TOKENS=3000
```

The formatter:

- reserves space for note headers and separators
- distributes content fairly across notes
- redistributes unused space from short notes to long notes
- includes note type, date, and tags when available
- omits UUIDs from the model prompt because the server already owns the
  positional mapping
- truncates with the ellipsis inside the budget
- preserves one-based markers `[1]`, `[2]`, and so on

Each note is represented approximately like this:

```text
[1] (decision; 2026-08-04; tags: search, architecture)
The note content appears here...
```

An aggregate budget prevents silent Ollama context truncation. Without that
limit, the model could receive a prompt larger than its context window, lose
the leading notes, and still produce citations using the original numbers.

## 6. Grounded Answer Generation

The second model call is `StreamAnswer` in
`internal/converse/converse.go`.

The prompt contains:

- the original user question
- the rewritten retrieval query
- the numbered, budgeted notes
- instructions to answer directly from those notes
- instructions to cite supporting notes as `[n]` or `[n, m]`
- instructions to say when the notes are insufficient
- instructions to mention conflicts and cite both sides

The Ollama request uses `/api/generate` with streaming enabled. Each response
chunk is immediately passed to the WebSocket handler through `onChunk`, which
sends a `conversation_chunk` message to the browser.

The final model response is also retained as a complete string for citation
verification.

## 7. Citation Verification

Citation handling is in `internal/converse/citations.go`.

The verifier:

1. Finds citations such as `[1]`, `[1, 2]`, and `[1,2,3]`.
2. Extracts every number, including all numbers in multi-citations.
3. Removes references to note numbers that were not supplied.
4. Narrows the returned source list to the notes actually cited.
5. Renumbers the remaining sources so the final invariant is:

```text
answer citation [n] == returned Sources[n-1]
```

For example, if the model cites retrieved notes `[2]` and `[4]`, the server
returns those two notes as sources and rewrites the answer citations to `[1]`
and `[2]`.

If the model does not cite anything, the answer is preserved and all retrieved
notes remain available for audit. This is safer than pretending the answer had
no grounding, but the UI does not claim that every retrieved note was used.

## 8. WebSocket Response and UI

The final response has `intent: "conversation"` and `mode:
"conversational"`. It contains:

- `content`: the verified answer
- `sources`: the cited notes, serialized as search results
- `conversation_details.notes_read`: all notes shown to the model
- `conversation_details.notes_cited`: notes cited by the final answer
- `conversation_details.search_rounds`: retrieval rounds used
- `conversation_details.search_query`: rewritten retrieval query

The browser renders the final answer with Markdown-safe DOM construction. Each
number in a multi-citation such as `[1, 2]` is a separate clickable source
control. The source drawer opens the corresponding note detail view.

The UI also displays provenance such as:

```text
cited 3 of 8 notes read · searched for "search timeout decision"
```

Streaming text is shown as plain text while generation is in progress. The
final verified answer is rendered after the complete response arrives so
citation buttons are only created after source reconciliation.

## 9. Why This Replaced the Original Loop

The original conversational implementation used this sequence:

```text
rewrite -> retrieve 3 notes -> ask the model ENOUGH/MORE
       -> embed again -> retrieve 3 more -> ask ENOUGH/MORE
       -> repeat until the model stopped or 10 notes were read
       -> answer
```

That design was poorly matched to local models because:

- the sufficiency gate added several expensive generations
- an ambiguous gate response defaulted to requesting more notes
- the same query was embedded repeatedly
- embedding and generation alternated, encouraging model eviction
- each note had a limit but the complete prompt did not
- the model could spend many hidden reasoning tokens on a one-word decision

The current design keeps the useful parts, namely query understanding,
multi-source retrieval, grounded generation, streaming, and citations, while
removing the unbounded control loop.

## 10. Configuration Reference

The conversational settings are defined in `internal/config/config.go` and
documented in `.env.example`.

| Variable | Default | Purpose |
|---|---:|---|
| `OPENBRAIN_CONVERSE_MODEL` | empty | Primary local answer/rewrite model; takes precedence over extraction models |
| `OPENBRAIN_CONVERSE_REWRITE_MODEL` | primary model | Optional separate rewrite model; usually leave empty to avoid another model load |
| `OPENBRAIN_CONVERSE_MAX_NOTES` | `8` | Maximum notes selected for grounding |
| `OPENBRAIN_CONVERSE_PROMPT_TOKENS` | `3000` | Aggregate note prompt budget |
| `OPENBRAIN_CONVERSE_NUM_CTX` | `8192` | Ollama context option |
| `OPENBRAIN_CONVERSE_MAX_ROUNDS` | `1` | Retrieval rounds; one is the fast deterministic default |
| `OPENBRAIN_CONVERSE_KEEP_ALIVE` | `30m` | Ollama model residency between calls |
| `OPENBRAIN_CONVERSE_REWRITE_TIMEOUT` | `25s` | Rewrite-call timeout |
| `OPENBRAIN_CONVERSE_ANSWER_TIMEOUT` | `90s` | Answer-call timeout |
| `OPENBRAIN_CONVERSE_WARM_MODEL` | `false` | Optional one-token startup warm-up |
| `OPENBRAIN_SEARCH_AGENT_TIMEOUT` | `90s` | Whole-operation backstop |

The live configuration in this checkout uses the pulled MLX model:

```env
OPENBRAIN_CONVERSE_MODEL=qwen3.5:4b-mlx
OPENBRAIN_EMBEDDING_MODEL=qwen3-embedding:0.6b
```

## 11. Relevant Source Files

- `internal/brain/brain.go`: orchestration, deterministic retrieval, result
  metadata, and the `ConverseSearch` entry point
- `internal/converse/converse.go`: Ollama provider, prompts, streaming,
  timeouts, model preflight, and generation options
- `internal/converse/notes.go`: aggregate prompt budgeting and note formatting
- `internal/converse/citations.go`: citation parsing, validation, and source
  reconciliation
- `internal/rankfuse/rankfuse.go`: reciprocal-rank fusion
- `internal/db/search.go`: PostgreSQL keyword and hybrid retrieval
- `cmd/openbrain-web/handler.go`: WebSocket mode routing and response
  serialization
- `cmd/openbrain-web/static/composer.js`: Semantic and Conversational strategy
  controls and tooltips
- `cmd/openbrain-web/static/index.html`: streaming answer rendering, source
  drawer, clickable citations, and conversation metadata

## 12. Testing

The implementation has focused tests for:

- Ollama request options and model preflight
- streaming and oversized response lines
- timeout and cancellation errors
- rewrite sanitization and fallback behavior
- aggregate prompt budgets and fair note allocation
- citation parsing, multi-citations, hallucinated indices, and renumbering
- score-gap cutoff and duplicate filtering

The relevant test files are under `internal/converse/` and
`internal/brain/converse_test.go`.
