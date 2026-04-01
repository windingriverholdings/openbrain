// Package brain is the core dispatcher that routes parsed intents to the
// appropriate action handlers (capture, search, review, etc.).
package brain

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/craig8/openbrain/internal/config"
	"github.com/craig8/openbrain/internal/db"
	"github.com/craig8/openbrain/internal/embeddings"
	"github.com/craig8/openbrain/internal/extract"
	"github.com/craig8/openbrain/internal/intent"
	"github.com/craig8/openbrain/internal/llm"
	"github.com/craig8/openbrain/internal/model"
)

// Brain orchestrates intent dispatch using an embedder and database pool.
type Brain struct {
	pool     *pgxpool.Pool
	embedder embeddings.Embedder
	cfg      *config.Config
}

// New creates a Brain with the given dependencies.
func New(pool *pgxpool.Pool, embedder embeddings.Embedder, cfg *config.Config) *Brain {
	return &Brain{pool: pool, embedder: embedder, cfg: cfg}
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
	embedding, err := b.embedder.Embed(ctx, parsed.Text)
	if err != nil {
		return "", fmt.Errorf("embed thought: %w", err)
	}

	id, err := db.InsertThought(ctx, b.pool, parsed.Text, embedding, parsed.ThoughtType, parsed.Tags, source, nil, nil)
	if err != nil {
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
}

// filteredSearchMinThreshold is the default minimum score threshold used when
// a type filter is applied, since filtered searches on small corpora need more
// lenient scoring than unfiltered searches.
const filteredSearchMinThreshold = 0.01

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
	embedding, err := b.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	filteredThresh := b.cfg.SearchFilteredThreshold
	if filteredThresh == 0 {
		filteredThresh = filteredSearchMinThreshold
	}
	threshold := effectiveThreshold(b.cfg.SearchScoreThreshold, filteredThresh, opts)

	switch opts.Mode {
	case "keyword":
		return db.KeywordSearchThoughts(ctx, b.pool, query, b.cfg.SearchTopK, opts.IncludeHistory, opts.ThoughtType)
	case "vector":
		return db.SearchThoughts(ctx, b.pool, embedding, b.cfg.SearchTopK, opts.ThoughtType, opts.Tags, threshold)
	default:
		return db.HybridSearchThoughts(ctx, b.pool, query, embedding, b.cfg.SearchTopK, 0.3, 0.7, threshold, opts.IncludeHistory, opts.ThoughtType)
	}
}

// GetStats returns aggregate brain statistics.
func (b *Brain) GetStats(ctx context.Context) (*model.Stats, error) {
	return db.GetStats(ctx, b.pool)
}

// GetReview returns thoughts from the past N days.
func (b *Brain) GetReview(ctx context.Context, days int) ([]model.ThoughtRow, error) {
	return db.GetThoughtsSince(ctx, b.pool, days)
}

// Supersede captures a new thought and marks an older thought as superseded.
// If parsed.OldThoughtID is set, that thought is superseded directly (no search).
// If parsed.SupersedeQuery is set, it is embedded to find the best match instead
// of using the new thought's own embedding.
func (b *Brain) Supersede(ctx context.Context, parsed intent.ParsedIntent, source string) (string, error) {
	embedding, err := b.embedder.Embed(ctx, parsed.Text)
	if err != nil {
		return "", fmt.Errorf("embed supersede: %w", err)
	}

	newID, err := db.InsertThought(ctx, b.pool, parsed.Text, embedding, parsed.ThoughtType, parsed.Tags, source, nil, nil)
	if err != nil {
		return "", err
	}

	// Direct supersede: caller provided the exact old thought ID.
	if parsed.OldThoughtID != nil {
		oldID := *parsed.OldThoughtID
		if err := db.SupersedeThought(ctx, b.pool, oldID, newID); err != nil {
			slog.Warn("supersede failed", "error", err)
			return fmt.Sprintf("Captured [%s] %s (supersede failed)", parsed.ThoughtType, newID[:8]), nil
		}
		return fmt.Sprintf("Captured [%s] %s — supersedes %s", parsed.ThoughtType, newID[:8], oldID[:8]), nil
	}

	// Search-based supersede: embed the query (or new content) to find the best match.
	searchEmbedding := embedding
	if parsed.SupersedeQuery != nil {
		searchEmbedding, err = b.embedder.Embed(ctx, *parsed.SupersedeQuery)
		if err != nil {
			return fmt.Sprintf("Captured [%s] %s (embed query failed)", parsed.ThoughtType, newID[:8]), nil
		}
	}

	results, err := db.SearchThoughts(ctx, b.pool, searchEmbedding, 1, "", nil, 0.3)
	if err != nil || len(results) == 0 {
		return fmt.Sprintf("Captured [%s] %s (no match to supersede)", parsed.ThoughtType, newID[:8]), nil
	}

	oldID := results[0].ID
	if oldID == newID {
		return fmt.Sprintf("Captured [%s] %s (no prior match)", parsed.ThoughtType, newID[:8]), nil
	}

	if err := db.SupersedeThought(ctx, b.pool, oldID, newID); err != nil {
		slog.Warn("supersede failed", "error", err)
		return fmt.Sprintf("Captured [%s] %s (supersede failed)", parsed.ThoughtType, newID[:8]), nil
	}

	return fmt.Sprintf("Captured [%s] %s — supersedes %s", parsed.ThoughtType, newID[:8], oldID[:8]), nil
}

// DeepCapture extracts multiple thoughts from long text via LLM.
// Uses the shared captureExtracted helper (also used by DeepCaptureWithMeta).
func (b *Brain) DeepCapture(ctx context.Context, parsed intent.ParsedIntent, source string) (string, error) {
	candidates, err := extract.ExtractThoughts(ctx, parsed.Text)
	if err != nil {
		slog.Warn("extraction failed, falling back to simple capture", "error", err)
		return b.Capture(ctx, parsed, source)
	}

	if len(candidates) == 0 {
		return b.Capture(ctx, parsed, source)
	}

	captured, errs := captureExtracted(ctx, b, candidates, source, nil)
	return fmt.Sprintf("Extracted %d thoughts: %s", len(captured), formatCaptureResult(captured, errs)), nil
}

// --- Formatting helpers (text output for CLI/chat) ---

func (b *Brain) reload() (string, error) {
	config.Reload()
	llm.ResetProviders()
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

	if len(results) == 0 {
		return "No matching thoughts found.", nil
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

	return sb.String(), nil
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
