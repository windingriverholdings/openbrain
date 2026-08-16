package brain

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/windingriverholdings/openbrain/internal/config"
	"github.com/windingriverholdings/openbrain/internal/model"
	"github.com/windingriverholdings/openbrain/internal/rankfuse"
)

type testSummarizer struct {
	text  string
	query string
	rows  []model.ThoughtRow
	err   error
}

func (s *testSummarizer) Summarize(_ context.Context, query string, rows []model.ThoughtRow) (string, error) {
	s.query = query
	s.rows = rows
	return s.text, s.err
}

func TestSummarizeSearchUsesRetrievedRowsWithoutMutation(t *testing.T) {
	provider := &testSummarizer{text: "Caching decisions favor the local cache."}
	b := &Brain{}
	b.SetSummarizerForTesting(provider)
	rows := []model.ThoughtRow{{ID: "one", Content: "Use a local cache"}}

	got, err := b.SummarizeSearch(context.Background(), "caching decisions", rows)
	assert.NoError(t, err)
	assert.Equal(t, "Caching decisions favor the local cache.", got.Summary)
	assert.Equal(t, 1, got.ResultCount)
	assert.Equal(t, "caching decisions", provider.query)
	assert.Equal(t, rows, provider.rows)
	assert.Equal(t, "Use a local cache", rows[0].Content)
}

func TestSummarizeSearchReturnsProviderError(t *testing.T) {
	provider := &testSummarizer{err: errors.New("model unavailable")}
	b := &Brain{}
	b.SetSummarizerForTesting(provider)

	_, err := b.SummarizeSearch(context.Background(), "query", []model.ThoughtRow{{ID: "one"}})
	assert.EqualError(t, err, "model unavailable")
}

type testStreamSummarizer struct {
	testSummarizer
	chunks []string
}

func (s *testStreamSummarizer) StreamSummarize(_ context.Context, query string, rows []model.ThoughtRow, onChunk func(string) error) (string, error) {
	s.query = query
	s.rows = rows
	if s.err != nil {
		return "", s.err
	}
	for _, chunk := range s.chunks {
		if err := onChunk(chunk); err != nil {
			return "", err
		}
	}
	return s.text, nil
}

func TestSummarizeSearchStream_SupportsStreaming(t *testing.T) {
	provider := &testStreamSummarizer{
		testSummarizer: testSummarizer{text: "Complete text"},
		chunks:         []string{"Comp", "lete", " text"},
	}
	b := &Brain{}
	b.SetSummarizerForTesting(provider)

	var received []string
	onChunk := func(chunk string) error {
		received = append(received, chunk)
		return nil
	}

	got, err := b.SummarizeSearchStream(context.Background(), "query", []model.ThoughtRow{{ID: "one"}}, onChunk)
	assert.NoError(t, err)
	assert.Equal(t, "Complete text", got.Summary)
	assert.Equal(t, []string{"Comp", "lete", " text"}, received)
}

func TestSummarizeSearchStream_Fallback(t *testing.T) {
	provider := &testSummarizer{text: "Standard text"}
	b := &Brain{}
	b.SetSummarizerForTesting(provider)

	var received []string
	onChunk := func(chunk string) error {
		received = append(received, chunk)
		return nil
	}

	got, err := b.SummarizeSearchStream(context.Background(), "query", []model.ThoughtRow{{ID: "one"}}, onChunk)
	assert.NoError(t, err)
	assert.Equal(t, "Standard text", got.Summary)
	assert.Equal(t, []string{"Standard text"}, received)
}

func TestSearchOptsDefaults(t *testing.T) {
	opts := SearchOpts{}
	assert.Equal(t, "", opts.Mode)
	assert.Equal(t, "", opts.ThoughtType)
	assert.Nil(t, opts.Tags)
	assert.False(t, opts.IncludeHistory)
}

func TestSearchOptsWithAllFields(t *testing.T) {
	opts := SearchOpts{
		Mode:           "hybrid",
		ThoughtType:    "decision",
		Tags:           []string{"architecture", "backend"},
		IncludeHistory: true,
	}
	assert.Equal(t, "hybrid", opts.Mode)
	assert.Equal(t, "decision", opts.ThoughtType)
	assert.Equal(t, []string{"architecture", "backend"}, opts.Tags)
	assert.True(t, opts.IncludeHistory)
}

func TestEffectiveThresholdUsesCustomFilteredValue(t *testing.T) {
	// When a custom filtered threshold is provided, it should be used
	// instead of the default constant.
	opts := SearchOpts{ThoughtType: "insight"}
	customThreshold := 0.05
	threshold := effectiveThreshold(0.15, customThreshold, opts)
	assert.Equal(t, customThreshold, threshold)
}

func TestEffectiveThresholdLoweredForTypeFilter(t *testing.T) {
	// When ThoughtType is set, the effective score threshold should be
	// lowered to 0.01 to avoid filtering out valid typed results.
	opts := SearchOpts{
		ThoughtType: "decision",
	}
	threshold := effectiveThreshold(0.15, filteredSearchMinThreshold, opts)
	assert.Equal(t, filteredSearchMinThreshold, threshold)
}

func TestEffectiveThresholdUnchangedWithoutTypeFilter(t *testing.T) {
	opts := SearchOpts{}
	threshold := effectiveThreshold(0.15, filteredSearchMinThreshold, opts)
	assert.Equal(t, 0.15, threshold)
}

// ── TopK resolution ─────────────────────────────────────────────────────────
//
// Search depth is configurable per call so assisted search can retrieve more
// deeply than a plain search without changing the default for every caller.

// TestResolveTopK_ExplicitOverrideWins asserts a per-call TopK beats config on
// every mode, including assisted.
func TestResolveTopK_ExplicitOverrideWins(t *testing.T) {
	b := &Brain{cfg: &config.Config{SearchTopK: 10, SearchAssistedTopK: 25}}

	assert.Equal(t, 7, b.resolveTopK(SearchOpts{TopK: 7}))
	assert.Equal(t, 7, b.resolveTopK(SearchOpts{Mode: "assisted", TopK: 7}))
}

// TestResolveTopK_ZeroFallsBackToConfig pins the backward-compatible default:
// callers that never set TopK keep the configured behavior.
func TestResolveTopK_ZeroFallsBackToConfig(t *testing.T) {
	b := &Brain{cfg: &config.Config{SearchTopK: 10, SearchAssistedTopK: 25}}

	assert.Equal(t, 10, b.resolveTopK(SearchOpts{}))
	assert.Equal(t, 10, b.resolveTopK(SearchOpts{Mode: "hybrid"}))
	assert.Equal(t, 10, b.resolveTopK(SearchOpts{Mode: "keyword"}))
	assert.Equal(t, 10, b.resolveTopK(SearchOpts{Mode: "vector"}))
}

// TestResolveTopK_AssistedRetrievesDeeper asserts assisted search uses its own
// larger cap, since it fuses several ranked lists and needs candidates from
// each one to have anything to agree about.
func TestResolveTopK_AssistedRetrievesDeeper(t *testing.T) {
	b := &Brain{cfg: &config.Config{SearchTopK: 10, SearchAssistedTopK: 25}}
	assert.Equal(t, 25, b.resolveTopK(SearchOpts{Mode: "assisted"}))
}

// TestResolveTopK_UnconfiguredNeverReturnsZero guards the failure mode where a
// zero-valued config silently makes every search return no rows.
func TestResolveTopK_UnconfiguredNeverReturnsZero(t *testing.T) {
	b := &Brain{cfg: &config.Config{}}

	assert.Equal(t, defaultSearchTopK, b.resolveTopK(SearchOpts{}))
	assert.Equal(t, defaultSearchTopK, b.resolveTopK(SearchOpts{Mode: "assisted"}),
		"assisted search with no configured cap must still return rows")
}

// TestRRFK_DefaultsWhenUnconfigured asserts the fusion constant is always valid,
// so a missing or nonsensical config cannot produce a zero divisor.
func TestRRFK_DefaultsWhenUnconfigured(t *testing.T) {
	assert.Equal(t, rankfuse.DefaultK, (&Brain{cfg: &config.Config{}}).rrfK())
	assert.Equal(t, rankfuse.DefaultK, (&Brain{}).rrfK())
	assert.Equal(t, 90, (&Brain{cfg: &config.Config{SearchRRFK: 90}}).rrfK())
}
