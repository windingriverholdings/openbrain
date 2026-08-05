// Package brain is the core dispatcher that routes parsed intents to the
// appropriate action handlers (capture, search, review, etc.).
package brain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/windingriverholdings/openbrain/internal/config"
	"github.com/windingriverholdings/openbrain/internal/db"
	"github.com/windingriverholdings/openbrain/internal/embeddings"
	"github.com/windingriverholdings/openbrain/internal/extract"
	"github.com/windingriverholdings/openbrain/internal/intent"
	"github.com/windingriverholdings/openbrain/internal/model"
	"github.com/windingriverholdings/openbrain/internal/queryexpand"
	"github.com/windingriverholdings/openbrain/internal/rankfuse"
	"github.com/windingriverholdings/openbrain/internal/searchplan"
	"github.com/windingriverholdings/openbrain/internal/summarize"
)

// Brain orchestrates intent dispatch using an embedder and database pool.
type Brain struct {
	pool       *pgxpool.Pool
	embedder   embeddings.Embedder
	cfg        *config.Config
	summarizer summarize.Provider
	expander   queryexpand.Provider
	planner    searchplan.Planner

	// extractFn and captureFn are seams over the LLM extraction call and the
	// single-note fallback capture, defaulted in New to the real
	// implementations. They exist so DeepCapture's loud-fallback behavior is
	// testable without a live LLM or database.
	extractFn func(ctx context.Context, text string) ([]extract.Candidate, error)
	captureFn func(ctx context.Context, parsed intent.ParsedIntent, source string) (string, error)

	// supersedeFn is a seam over the atomic capture-and-retire transaction,
	// defaulted in New to db.SupersedeCapture. It exists so the atomicity and
	// concurrency contract of Supersede is testable without a live database.
	supersedeFn func(ctx context.Context, params db.SupersedeParams) (string, error)

	// supersedeSearchFn is a seam over the search call resolveSupersedeTarget
	// uses to find the best prior match for a search-based supersede,
	// defaulted in New to db.SearchThoughts. It exists so all three branches
	// (search error, no match, match found) are testable without a live
	// database.
	supersedeSearchFn func(ctx context.Context, embedding []float32) ([]model.ThoughtRow, error)

	// bulkInsertFn is a seam over the atomic multi-thought insert, defaulted in
	// New to db.BulkInsertThoughts. It backs both BulkImport and the
	// extract-then-auto_capture path, so their all-or-nothing contract is
	// testable without a live database.
	bulkInsertFn func(ctx context.Context, inputs []db.ThoughtInput) ([]string, error)

	// storeFn is a seam over the single-thought persist call (db.InsertThought),
	// defaulted in New to a closure bound to b.pool. It exists so Capture's
	// full path, including the post-embed success outcome, is testable
	// without a live database, mirroring the bulkInsertFn/supersedeFn seams.
	storeFn func(ctx context.Context, content string, embedding []float32, thoughtType string, tags []string, source string) (string, error)
}

// New creates a Brain with the given dependencies.
func New(pool *pgxpool.Pool, embedder embeddings.Embedder, cfg *config.Config) *Brain {
	b := &Brain{pool: pool, embedder: embedder, cfg: cfg}
	if summarizerProvider, err := summarize.New(cfg); err == nil {
		b.summarizer = summarizerProvider
	} else {
		slog.Warn("search summarization unavailable", "error", err)
	}
	if expander, err := queryexpand.New(cfg); err == nil {
		b.expander = expander
	} else {
		slog.Warn("contextual search expansion unavailable", "error", err)
	}
	if planner, err := searchplan.NewPlanner(cfg); err == nil {
		b.planner = planner
	} else {
		slog.Warn("AI Search unavailable", "error", err)
	}
	b.extractFn = extract.ExtractThoughts
	b.captureFn = b.Capture
	b.supersedeFn = func(ctx context.Context, params db.SupersedeParams) (string, error) {
		return db.SupersedeCapture(ctx, b.pool, params)
	}
	b.supersedeSearchFn = func(ctx context.Context, embedding []float32) ([]model.ThoughtRow, error) {
		return db.SearchThoughts(ctx, b.pool, embedding, 1, "", nil, 0.3, nil, nil)
	}
	b.bulkInsertFn = func(ctx context.Context, inputs []db.ThoughtInput) ([]string, error) {
		return db.BulkInsertThoughts(ctx, b.pool, inputs)
	}
	b.storeFn = func(ctx context.Context, content string, embedding []float32, thoughtType string, tags []string, source string) (string, error) {
		return db.InsertThought(ctx, b.pool, content, embedding, thoughtType, tags, source, nil, nil)
	}
	return b
}

// DeleteThought permanently removes a thought from the brain.
func (b *Brain) DeleteThought(ctx context.Context, id string) error {
	return db.DeleteThought(ctx, b.pool, id)
}

// SetSeamsForTesting overrides the extract and bulk-insert seams from test
// code in OTHER packages (e.g. internal/mcptools), so the MCP handler layer
// can be exercised without a live LLM or database. A nil argument leaves the
// corresponding seam untouched. Production code must never call this: it
// exists solely so handler tests can inject the same deterministic
// extract/store behavior that this package's own tests already reach via the
// unexported extractFn/bulkInsertFn fields.
func (b *Brain) SetSeamsForTesting(
	extractFn func(ctx context.Context, text string) ([]extract.Candidate, error),
	bulkInsertFn func(ctx context.Context, inputs []db.ThoughtInput) ([]string, error),
) {
	if extractFn != nil {
		b.extractFn = extractFn
	}
	if bulkInsertFn != nil {
		b.bulkInsertFn = bulkInsertFn
	}
}

// SetStoreFnForTesting overrides the single-thought store seam from test code
// in OTHER packages (e.g. internal/mcptools), so Capture's full success path
// is exercisable without a live database. Production code must never call
// this. See SetSeamsForTesting for the equivalent extract/bulk-insert seams.
func (b *Brain) SetStoreFnForTesting(
	storeFn func(ctx context.Context, content string, embedding []float32, thoughtType string, tags []string, source string) (string, error),
) {
	if storeFn != nil {
		b.storeFn = storeFn
	}
}

// SetSummarizerForTesting injects a deterministic search summarizer for tests.
func (b *Brain) SetSummarizerForTesting(provider summarize.Provider) {
	b.summarizer = provider
}

// SetQueryExpanderForTesting injects a deterministic contextual search expander.
func (b *Brain) SetQueryExpanderForTesting(provider queryexpand.Provider) {
	b.expander = provider
}

// SetSearchPlannerForTesting injects a deterministic AI Search planner.
func (b *Brain) SetSearchPlannerForTesting(planner searchplan.Planner) {
	b.planner = planner
}

// Dispatch routes a parsed intent to the appropriate handler.
func (b *Brain) Dispatch(ctx context.Context, parsed intent.ParsedIntent, source string) (string, error) {
	switch parsed.Intent {
	case intent.Help:
		return intent.HelpText, nil
	case intent.Reload:
		return b.reload()
	case intent.Stats:
		return b.formatStats(ctx)
	case intent.Review:
		return b.formatReview(ctx, 7)
	case intent.Search:
		return b.formatSearch(ctx, parsed.Text, SearchOpts{Mode: "hybrid"})
	case intent.Ambiguous:
		// Non-interactive callers cannot ask the user to choose. Search is
		// the safe default because it is read-only; the web UI presents the
		// explicit capture/search choice instead.
		return b.formatSearch(ctx, parsed.Text, SearchOpts{Mode: "hybrid"})
	case intent.Supersede:
		return b.Supersede(ctx, parsed, source)
	case intent.Extract:
		return b.DeepCapture(ctx, parsed, source)
	case intent.Capture:
		return b.Capture(ctx, parsed, source)
	default:
		return "", fmt.Errorf("unknown intent: %s", parsed.Intent)
	}
}

// Capture stores a single thought with embedding and subject linking.
func (b *Brain) Capture(ctx context.Context, parsed intent.ParsedIntent, source string) (string, error) {
	if err := requireNonEmptyText("capture", parsed.Text); err != nil {
		return "", err
	}

	embedding, err := b.embedder.Embed(ctx, parsed.Text)
	if err != nil {
		slog.Error("capture: embed failed", "source", source, "thought_type", parsed.ThoughtType, "content_len", len(parsed.Text), "error", err)
		return "", fmt.Errorf("embed thought: %w", err)
	}

	id, err := b.storeFn(ctx, parsed.Text, embedding, parsed.ThoughtType, parsed.Tags, source)
	if err != nil {
		slog.Error("capture: store failed", "source", source, "thought_type", parsed.ThoughtType, "error", err)
		return "", err
	}

	subjects := extractSubjectsSimple(parsed.Text, parsed.ThoughtType, parsed.Tags)
	if len(subjects) > 0 {
		if err := db.LinkSubjects(ctx, b.pool, id, subjects); err != nil {
			slog.Warn("failed to link subjects", "error", err)
		}
	}

	return fmt.Sprintf("Captured [%s] %s (%s)", parsed.ThoughtType, id[:8], source), nil
}

// SearchOpts holds optional filters for search operations.
type SearchOpts struct {
	Mode           string
	ThoughtType    string
	Tags           []string
	IncludeHistory bool
	CreatedFrom    *time.Time // inclusive lower bound on created_at; nil = unbounded
	CreatedTo      *time.Time // inclusive upper bound on created_at; nil = unbounded

	// TopK caps the number of returned rows. Zero means "use the configured
	// default", so existing callers keep their current behavior.
	TopK int
}

// CaptureDetails describes the work performed by AI-assisted storage.
type CaptureDetails struct {
	OriginalPreserved bool                `json:"original_preserved"`
	Extracted         []extract.Candidate `json:"extracted,omitempty"`
}

// SearchDetails contains retrieval results plus a description of the retrieval
// work performed. AxesUsed names the independent retrieval strategies whose
// rankings were fused, and is populated only for assisted searches.
type SearchDetails struct {
	Results   []model.ThoughtRow
	AxesUsed  []string
	Expansion *SearchExpansion
}

// SearchExpansion records the model's grounded interpretation of an assisted
// search. It is nil when expansion was unavailable, declined, or not requested.
type SearchExpansion struct {
	Interpretation string
	Terms          []string
	Confidence     float64
}

// SummaryDetails describes a read-only summary of retrieved search results.
type SummaryDetails struct {
	Summary     string
	ResultCount int
	ModelUsed   string
}

// SummarizeSearch summarizes the supplied results without changing retrieval or storage.
func (b *Brain) SummarizeSearch(ctx context.Context, query string, results []model.ThoughtRow) (SummaryDetails, error) {
	if b.summarizer == nil {
		return SummaryDetails{}, fmt.Errorf("search summarization is unavailable: configure a local LLM model")
	}
	text, err := b.summarizer.Summarize(ctx, query, results)
	if err != nil {
		return SummaryDetails{}, err
	}
	details := SummaryDetails{Summary: text, ResultCount: len(results)}
	if named, ok := b.summarizer.(summarize.ModelNamer); ok {
		details.ModelUsed = named.ModelName()
	}
	return details, nil
}

// filteredSearchMinThreshold is the default minimum score threshold used when
// a type filter is applied, since filtered searches on small corpora need more
// lenient scoring than unfiltered searches.
const filteredSearchMinThreshold = 0.01

// defaultSearchTopK is the last-resort row cap when configuration supplies no
// value, so a zero-valued config can never silently return no rows.
const defaultSearchTopK = 10

// effectiveThreshold returns a lowered score threshold when a type filter
// is applied, since filtered searches on small corpora need more lenient scoring.
func effectiveThreshold(base float64, filteredThreshold float64, opts SearchOpts) float64 {
	if opts.ThoughtType != "" {
		return filteredThreshold
	}
	return base
}

// Search performs a search and returns structured results.
//
// NOTE: Tags filtering (opts.Tags) is currently only applied in vector mode.
// Keyword and hybrid searches ignore tags — this is a known limitation that
// should be addressed when those query paths gain tag support in the DB layer.
func (b *Brain) Search(ctx context.Context, query string, opts SearchOpts) ([]model.ThoughtRow, error) {
	details, err := b.SearchWithDetails(ctx, query, opts)
	if err != nil {
		return nil, err
	}
	return details.Results, nil
}

// SearchWithDetails performs a search and exposes assisted-search query
// expansion for interfaces that want to show the retrieval work.
func (b *Brain) SearchWithDetails(ctx context.Context, query string, opts SearchOpts) (SearchDetails, error) {
	if err := requireNonEmptyText("search", query); err != nil {
		return SearchDetails{}, err
	}

	filteredThresh := b.cfg.SearchFilteredThreshold
	if filteredThresh == 0 {
		filteredThresh = filteredSearchMinThreshold
	}
	threshold := effectiveThreshold(b.cfg.SearchScoreThreshold, filteredThresh, opts)
	topK := b.resolveTopK(opts)

	switch opts.Mode {
	case "ai", "agentic":
		return b.aiSearch(ctx, query, opts, threshold)
	case "keyword":
		rows, err := db.KeywordSearchThoughts(ctx, b.pool, query, topK, opts.IncludeHistory, opts.ThoughtType, opts.CreatedFrom, opts.CreatedTo)
		return SearchDetails{Results: rows}, err
	case "assisted":
		return b.assistedSearch(ctx, query, opts, threshold)
	case "vector":
		embedding, err := b.embedder.Embed(ctx, query)
		if err != nil {
			slog.Error("search: embed failed", "mode", opts.Mode, "query_len", len(query), "error", err)
			return SearchDetails{}, fmt.Errorf("embed query: %w", err)
		}
		rows, err := db.SearchThoughts(ctx, b.pool, embedding, topK, opts.ThoughtType, opts.Tags, threshold, opts.CreatedFrom, opts.CreatedTo)
		return SearchDetails{Results: rows}, err
	default:
		embedding, err := b.embedder.Embed(ctx, query)
		if err != nil {
			slog.Error("search: embed failed", "mode", opts.Mode, "query_len", len(query), "error", err)
			return SearchDetails{}, fmt.Errorf("embed query: %w", err)
		}
		rows, err := db.HybridSearchThoughts(ctx, b.pool, query, embedding, topK, 0.3, 0.7, threshold, opts.IncludeHistory, opts.ThoughtType, opts.CreatedFrom, opts.CreatedTo, b.cfg.EmbeddingDim)
		return SearchDetails{Results: rows}, err
	}
}

// AISearchDetails contains the final rows and the safe searches the planner
// requested. The trace is intended for the UI so users can inspect AI Search.
type AISearchDetails struct {
	Results []model.ThoughtRow
	Steps   []searchplan.Step
}

func (b *Brain) aiSearch(ctx context.Context, query string, opts SearchOpts, threshold float64) (SearchDetails, error) {
	if b.planner == nil {
		return SearchDetails{}, fmt.Errorf("AI Search is unavailable: configure a local LLM model")
	}
	if b.cfg != nil {
		if timeout := b.cfg.SearchAgentTimeout; timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}
	plan, err := b.planner.Plan(ctx, query)
	if err != nil {
		return SearchDetails{}, err
	}
	rowsByID := make(map[string]model.ThoughtRow)
	steps := make([]searchplan.Step, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		searchOpts := opts
		searchOpts.Mode = step.Mode
		searchOpts.ThoughtType = step.ThoughtType
		searchOpts.Tags = step.Tags
		searchOpts.IncludeHistory = step.IncludeHistory
		searchOpts.TopK = step.TopK
		details, searchErr := b.SearchWithDetails(ctx, step.Query, searchOpts)
		if searchErr != nil {
			continue
		}
		steps = append(steps, step)
		for _, row := range details.Results {
			rowsByID[row.ID] = row
		}
	}
	if len(steps) == 0 {
		return SearchDetails{}, fmt.Errorf("AI Search could not complete any safe search")
	}
	rows := make([]model.ThoughtRow, 0, len(rowsByID))
	for _, row := range rowsByID {
		rows = append(rows, row)
	}
	// Keep output deterministic even though rowsByID is intentionally used for
	// deduplication.
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := 0.0, 0.0
		if rows[i].Score != nil {
			left = *rows[i].Score
		}
		if rows[j].Score != nil {
			right = *rows[j].Score
		}
		if left != right {
			return left > right
		}
		return rows[i].ID < rows[j].ID
	})
	topK := b.resolveTopK(opts)
	if len(rows) > topK {
		rows = rows[:topK]
	}
	names := make([]string, 0, len(steps))
	for _, step := range steps {
		names = append(names, step.Mode+": "+step.Query)
	}
	return SearchDetails{Results: rows, AxesUsed: names}, nil
}

// resolveTopK returns the row cap for a search: the explicit per-call TopK when
// set, otherwise the configured default. Assisted search retrieves more deeply
// because it fuses several ranked lists and needs candidates from each.
func (b *Brain) resolveTopK(opts SearchOpts) int {
	if opts.TopK > 0 {
		return opts.TopK
	}
	if opts.Mode == "vector" && b.cfg != nil && b.cfg.SearchFastTopK > 0 {
		return b.cfg.SearchFastTopK
	}
	if opts.Mode == "assisted" && b.cfg.SearchAssistedTopK > 0 {
		return b.cfg.SearchAssistedTopK
	}
	if b.cfg.SearchTopK > 0 {
		return b.cfg.SearchTopK
	}
	return defaultSearchTopK
}

// rrfK returns the reciprocal-rank-fusion damping constant.
func (b *Brain) rrfK() int {
	if b.cfg != nil && b.cfg.SearchRRFK > 0 {
		return b.cfg.SearchRRFK
	}
	return rankfuse.DefaultK
}

// assistedSearch grounds optional model expansion in an initial literal
// retrieval, then fuses the expansion with the exact-query retrieval axes.
// Model failures fall back to the deterministic lexical-plus-semantic path.
//
// Fusing by rank rather than by score matters because the two strategies emit
// incomparable numbers: full-text rank values are small (~0.05-0.3) while
// cosine similarity is large (~0.5-0.8). Summing them lets semantic similarity
// dominate, which buries exact matches on rare terms such as project
// codenames. Reciprocal rank fusion only reads positions, so a first-place
// lexical hit counts as much as a first-place semantic hit.
func (b *Brain) assistedSearch(ctx context.Context, query string, opts SearchOpts, threshold float64) (SearchDetails, error) {
	topK := b.resolveTopK(opts)
	if b.expander != nil {
		probeK := b.cfg.SearchProbeTopK
		if probeK <= 0 {
			probeK = 5
		}
		probeOpts := opts
		probeOpts.Mode = "hybrid"
		probeOpts.TopK = probeK
		probe, probeErr := b.literalAssistedSearch(ctx, query, probeOpts, threshold)
		if probeErr == nil && len(probe.Results) > 0 {
			expansion, expandErr := b.expander.Expand(ctx, query, probe.Results)
			if expandErr == nil && expansion.UseExpansion {
				expandedQuery := strings.Join(expansion.ExpandedTerms, " ")
				literal, literalErr := db.KeywordSearchThoughts(ctx, b.pool, query, topK, opts.IncludeHistory, opts.ThoughtType, opts.CreatedFrom, opts.CreatedTo)
				embedding, embedErr := b.embedder.Embed(ctx, expandedQuery)
				if literalErr == nil && embedErr == nil {
					semantic, semanticErr := db.HybridSearchThoughts(ctx, b.pool, expandedQuery, embedding, topK, 0.3, 0.7, threshold, opts.IncludeHistory, opts.ThoughtType, opts.CreatedFrom, opts.CreatedTo, b.cfg.EmbeddingDim)
					expandedLexical, lexicalErr := db.KeywordSearchThoughts(ctx, b.pool, expandedQuery, topK, opts.IncludeHistory, opts.ThoughtType, opts.CreatedFrom, opts.CreatedTo)
					if semanticErr == nil && lexicalErr == nil {
						results := rankfuse.FuseRRF(b.rrfK(), literal, semantic, expandedLexical)
						if len(results) > topK {
							results = results[:topK]
						}
						return SearchDetails{
							Results:  results,
							AxesUsed: []string{"literal", "contextual_expansion", "contextual_lexical"},
							Expansion: &SearchExpansion{
								Interpretation: expansion.Interpretation,
								Terms:          expansion.ExpandedTerms,
								Confidence:     expansion.Confidence,
							},
						}, nil
					}
				}
			}
		}
	}
	return b.literalAssistedSearch(ctx, query, opts, threshold)
}

func (b *Brain) literalAssistedSearch(ctx context.Context, query string, opts SearchOpts, threshold float64) (SearchDetails, error) {
	topK := b.resolveTopK(opts)

	type axis struct {
		name string
		rows []model.ThoughtRow
	}
	var axes []axis

	// Lexical axis: exact wording. This is the strongest available signal for
	// rare proper nouns, and the one the blended score used to discount.
	lexical, lexErr := db.KeywordSearchThoughts(ctx, b.pool, query, topK, opts.IncludeHistory, opts.ThoughtType, opts.CreatedFrom, opts.CreatedTo)
	if lexErr != nil {
		slog.Warn("assisted search: lexical axis failed", "error", lexErr)
	} else if len(lexical) > 0 {
		axes = append(axes, axis{name: "lexical", rows: lexical})
	}

	// Semantic axis: meaning, including paraphrases that share no keywords.
	embedding, embedErr := b.embedder.Embed(ctx, query)
	var semErr error
	if embedErr != nil {
		semErr = fmt.Errorf("embed query: %w", embedErr)
		slog.Warn("assisted search: semantic axis failed", "error", semErr)
	} else {
		semantic, err := db.HybridSearchThoughts(ctx, b.pool, query, embedding, topK, 0.3, 0.7, threshold, opts.IncludeHistory, opts.ThoughtType, opts.CreatedFrom, opts.CreatedTo, b.cfg.EmbeddingDim)
		if err != nil {
			semErr = err
			slog.Warn("assisted search: semantic axis failed", "error", err)
		} else if len(semantic) > 0 {
			axes = append(axes, axis{name: "semantic", rows: semantic})
		}
	}

	// Retrieval stays authoritative: only fail when every axis failed. An axis
	// that simply matched nothing is not an error.
	if lexErr != nil && semErr != nil {
		return SearchDetails{}, fmt.Errorf("assisted search: every retrieval axis failed: lexical: %v; semantic: %v", lexErr, semErr)
	}

	lists := make([][]model.ThoughtRow, 0, len(axes))
	names := make([]string, 0, len(axes))
	for _, a := range axes {
		lists = append(lists, a.rows)
		names = append(names, a.name)
	}

	results := rankfuse.FuseRRF(b.rrfK(), lists...)
	if len(results) > topK {
		results = results[:topK]
	}
	return SearchDetails{Results: results, AxesUsed: names}, nil
}

// GetStats returns aggregate brain statistics.
func (b *Brain) GetStats(ctx context.Context) (*model.Stats, error) {
	return db.GetStats(ctx, b.pool)
}

// GetReview returns thoughts from the past N days.
func (b *Brain) GetReview(ctx context.Context, days int) ([]model.ThoughtRow, error) {
	return db.GetThoughtsSince(ctx, b.pool, days)
}

// GetThought returns a single thought by UUID, or nil if not found.
func (b *Brain) GetThought(ctx context.Context, id string) (*model.ThoughtRow, error) {
	return db.GetThoughtByID(ctx, b.pool, id)
}

// Supersede captures a new thought and marks an older thought as superseded in
// one atomic transaction: either both writes commit or both roll back, so no
// orphan capture is ever left behind. On any failure it returns a real, typed
// error to the caller rather than a success-shaped confirmation string.
//
// If parsed.OldThoughtID is set, that thought is superseded directly (no
// search). Otherwise parsed.SupersedeQuery (or the new content) is embedded to
// find the best prior match; when there is no match the new thought is captured
// normally.
func (b *Brain) Supersede(ctx context.Context, parsed intent.ParsedIntent, source string) (string, error) {
	if err := requireNonEmptyText("supersede", parsed.Text); err != nil {
		return "", err
	}

	embedding, err := b.embedder.Embed(ctx, parsed.Text)
	if err != nil {
		slog.Error("supersede: embed content failed", "source", source, "content_len", len(parsed.Text), "error", err)
		return "", fmt.Errorf("embed supersede: %w", err)
	}

	oldID, err := b.resolveSupersedeTarget(ctx, parsed, embedding)
	if err != nil {
		return "", err
	}
	// No prior thought to supersede: capture the new thought normally. Routed
	// through captureFn (defaulted to Capture in New) rather than Capture
	// directly, so this fallback branch is testable without a live database.
	if oldID == "" {
		return b.captureFn(ctx, parsed, source)
	}

	params := db.SupersedeParams{
		Content:     parsed.Text,
		Embedding:   embedding,
		ThoughtType: parsed.ThoughtType,
		Tags:        parsed.Tags,
		Source:      source,
		OldID:       oldID,
	}
	newID, err := b.supersedeFn(ctx, params)
	if err != nil {
		slog.Error("supersede failed",
			"old_thought_id", oldID,
			"error", err)
		return "", fmt.Errorf("supersede thought %s: %w", db.ShortID(oldID), err)
	}

	slog.Info("thought superseded", "old", db.ShortID(oldID), "new", db.ShortID(newID))
	return fmt.Sprintf("Captured [%s] %s, supersedes %s",
		parsed.ThoughtType, db.ShortID(newID), db.ShortID(oldID)), nil
}

// resolveSupersedeTarget returns the id of the thought to retire. It returns an
// empty string (and nil error) when a search-based supersede finds no match,
// signaling the caller to capture the new thought normally.
func (b *Brain) resolveSupersedeTarget(ctx context.Context, parsed intent.ParsedIntent, embedding []float32) (string, error) {
	if parsed.OldThoughtID != nil {
		return *parsed.OldThoughtID, nil
	}

	searchEmbedding := embedding
	if parsed.SupersedeQuery != nil {
		if err := requireNonEmptyText("supersede query", *parsed.SupersedeQuery); err != nil {
			return "", err
		}
		var err error
		searchEmbedding, err = b.embedder.Embed(ctx, *parsed.SupersedeQuery)
		if err != nil {
			slog.Error("supersede: embed query failed", "query_len", len(*parsed.SupersedeQuery), "error", err)
			return "", fmt.Errorf("embed supersede query: %w", err)
		}
	}

	results, err := b.supersedeSearchFn(ctx, searchEmbedding)
	if err != nil {
		return "", fmt.Errorf("supersede search: %w", err)
	}
	if len(results) == 0 {
		return "", nil
	}
	return results[0].ID, nil
}

// DeepCapture stores the exact source text plus AI-derived thoughts. The
// original is always retained when extraction succeeds.
func (b *Brain) DeepCapture(ctx context.Context, parsed intent.ParsedIntent, source string) (string, error) {
	result, _, err := b.DeepCaptureWithDetails(ctx, parsed, source)
	return result, err
}

// DeepCaptureWithDetails stores the exact source and returns the extracted
// candidates so interfaces can show what AI Assist did.
func (b *Brain) DeepCaptureWithDetails(ctx context.Context, parsed intent.ParsedIntent, source string) (string, CaptureDetails, error) {
	if err := requireNonEmptyText("deep_capture", parsed.Text); err != nil {
		return "", CaptureDetails{}, err
	}

	candidates, err := b.extractFn(ctx, parsed.Text)
	if err != nil {
		// Loud fallback: still persist the raw note (never lose the user's
		// input), but make the degradation LOUD — log at Error and annotate
		// the returned confirmation so the user can tell from the response
		// alone that extraction did not happen.
		slog.Error("deep capture: extraction failed, stored as single note",
			"error", fmt.Errorf("deep capture: extraction failed: %w", err))
		confirmation, capErr := b.captureFn(ctx, parsed, source)
		if capErr != nil {
			return "", CaptureDetails{}, capErr
		}
		return fmt.Sprintf("⚠ extraction failed (%v) — stored as a single note: %s", err, confirmation), CaptureDetails{OriginalPreserved: true}, nil
	}

	if len(candidates) == 0 {
		confirmation, capErr := b.captureFn(ctx, parsed, source)
		return confirmation, CaptureDetails{OriginalPreserved: capErr == nil}, capErr
	}

	captureID, err := newCaptureID()
	if err != nil {
		return "", CaptureDetails{}, fmt.Errorf("deep capture: create capture id: %w", err)
	}
	captured, err := captureLosslessExtracted(ctx, b, parsed.Text, candidates, source, captureID)
	if err != nil {
		return "", CaptureDetails{}, fmt.Errorf("deep capture: %w", err)
	}
	return fmt.Sprintf("Stored the original note and extracted %d additional thoughts with AI Assist: %s", len(captured), strings.Join(captured, ", ")), CaptureDetails{
		OriginalPreserved: true,
		Extracted:         candidates,
	}, nil
}

func newCaptureID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// --- Formatting helpers (text output for CLI/chat) ---

func (b *Brain) reload() (string, error) {
	config.Reload()
	extract.ResetProviders()
	return "Configuration reloaded from .env", nil
}

func (b *Brain) formatStats(ctx context.Context) (string, error) {
	s, err := b.GetStats(ctx)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("OpenBrain Statistics\n")
	sb.WriteString(strings.Repeat("━", 20) + "\n")
	fmt.Fprintf(&sb, "Total thoughts : %d\n", s.Total)
	fmt.Fprintf(&sb, "This week      : %d\n", s.ThisWeek)
	fmt.Fprintf(&sb, "Today          : %d\n", s.Today)

	if s.OldestAt != nil {
		fmt.Fprintf(&sb, "Oldest thought : %s\n", s.OldestAt.Format("2006-01-02"))
	}
	if s.NewestAt != nil {
		fmt.Fprintf(&sb, "Newest thought : %s\n", s.NewestAt.Format("2006-01-02"))
	}

	if len(s.ByType) > 0 {
		sb.WriteString("\nBy type:\n")
		for typ, count := range s.ByType {
			fmt.Fprintf(&sb, "  %-12s %d\n", typ, count)
		}
	}

	return sb.String(), nil
}

func (b *Brain) formatReview(ctx context.Context, days int) (string, error) {
	thoughts, err := b.GetReview(ctx, days)
	if err != nil {
		return "", err
	}

	if len(thoughts) == 0 {
		return fmt.Sprintf("No thoughts captured in the past %d days.", days), nil
	}

	grouped := map[string][]model.ThoughtRow{}
	for _, t := range thoughts {
		grouped[t.ThoughtType] = append(grouped[t.ThoughtType], t)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Weekly Review (past %d days) — %d thoughts\n\n", days, len(thoughts))

	for typ, items := range grouped {
		fmt.Fprintf(&sb, "**%s** (%d)\n", capitalize(typ), len(items))
		for _, t := range items {
			fmt.Fprintf(&sb, "- %s\n", t.Content)
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

func (b *Brain) formatSearch(ctx context.Context, query string, opts SearchOpts) (string, error) {
	results, err := b.Search(ctx, query, opts)
	if err != nil {
		return "", err
	}

	return FormatSearchResults(results), nil
}

// FormatSearchResults renders search results as the plain-text listing used by
// the CLI, the MCP search tool, and the web socket's backward-compatible
// "content" field.
//
// It is exported so a caller that already holds the rows from Brain.Search can
// produce the exact same string without running the search (and therefore the
// query embedding) a second time. The output format is intentionally identical
// to what formatSearch has always emitted: existing callers must not observe a
// change.
func FormatSearchResults(results []model.ThoughtRow) string {
	if len(results) == 0 {
		return "No matching thoughts found."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d thought(s):\n\n", len(results))
	for i, t := range results {
		score := ""
		if t.Score != nil {
			score = fmt.Sprintf(" (%.2f)", *t.Score)
		}
		fmt.Fprintf(&sb, "%d. [%s]%s — %s\n   %s\n\n",
			i+1, t.ThoughtType, score, t.CreatedAt.Format("2006-01-02"), t.Content)
	}

	return sb.String()
}

func extractSubjectsSimple(text, thoughtType string, tags []string) []model.SubjectLink {
	var subjects []model.SubjectLink

	for _, tag := range tags {
		subjects = append(subjects, model.SubjectLink{Name: tag, Type: "tag"})
	}

	if thoughtType == "person" {
		words := strings.Fields(text)
		for i, w := range words {
			if strings.ToLower(w) == "met" && i+1 < len(words) {
				name := words[i+1]
				if i+2 < len(words) && len(words[i+2]) > 0 {
					first := rune(words[i+2][0])
					if unicode.IsUpper(first) {
						name += " " + words[i+2]
					}
				}
				name = strings.TrimRight(name, ".,;:!?")
				subjects = append(subjects, model.SubjectLink{Name: name, Type: "person"})
				break
			}
		}
	}

	return subjects
}

// capitalize returns s with the first letter uppercased.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
