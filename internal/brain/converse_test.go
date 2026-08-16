package brain

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/windingriverholdings/openbrain/internal/config"
	"github.com/windingriverholdings/openbrain/internal/converse"
	"github.com/windingriverholdings/openbrain/internal/model"
)

// fakeConverser is a deterministic conversational provider. It records what it
// was asked so tests can assert on prompt inputs without a live model.
type fakeConverser struct {
	rewrite      string
	rewriteErr   error
	answer       string
	answerErr    error
	gotQuestion  string
	gotSearchQ   string
	gotNotes     []model.ThoughtRow
	streamChunks []string
	gateEnough   bool
	gateErr      error
	gateCalls    int
}

func (f *fakeConverser) UnderstandQuery(_ context.Context, question string) (string, error) {
	f.gotQuestion = question
	if f.rewriteErr != nil {
		return "", f.rewriteErr
	}
	return f.rewrite, nil
}

func (f *fakeConverser) StreamAnswer(_ context.Context, question, searchQuery string, notes []model.ThoughtRow, onChunk func(string) error) (string, error) {
	f.gotQuestion = question
	f.gotSearchQ = searchQuery
	f.gotNotes = notes
	if f.answerErr != nil {
		return "", f.answerErr
	}
	for _, c := range f.streamChunks {
		if onChunk != nil {
			if err := onChunk(c); err != nil {
				return "", err
			}
		}
	}
	return f.answer, nil
}

func (f *fakeConverser) SufficiencyGate(_ context.Context, _, _ string, _ []model.ThoughtRow) (bool, error) {
	f.gateCalls++
	return f.gateEnough, f.gateErr
}

func score(v float64) *float64 { return &v }

func scoredRows(scores ...float64) []model.ThoughtRow {
	out := make([]model.ThoughtRow, len(scores))
	for i, s := range scores {
		out[i] = model.ThoughtRow{ID: string(rune('a' + i)), Score: score(s)}
	}
	return out
}

func TestConverseSearch_RejectsEmptyQuery(t *testing.T) {
	b := &Brain{converser: &fakeConverser{}}
	_, err := b.ConverseSearch(context.Background(), "   ", SearchOpts{}, nil, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyText)
}

func TestConverseSearch_UnavailableProviderIsActionable(t *testing.T) {
	b := &Brain{}
	_, err := b.ConverseSearch(context.Background(), "question?", SearchOpts{}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OPENBRAIN_CONVERSE_MODEL",
		"the error should name the knob that fixes it")
}

// TestApplyScoreGapCutoff covers the deterministic relevance cutoff that
// replaced the model-driven sufficiency gate.
func TestApplyScoreGapCutoff(t *testing.T) {
	tests := []struct {
		name  string
		rows  []model.ThoughtRow
		limit int
		want  int
	}{
		{
			name:  "sharp gap trims the tail",
			rows:  scoredRows(0.9, 0.85, 0.8, 0.05, 0.04),
			limit: 8,
			want:  3,
		},
		{
			name:  "flat scores are all kept",
			rows:  scoredRows(0.9, 0.88, 0.86, 0.84),
			limit: 8,
			want:  4,
		},
		{
			name:  "limit is respected",
			rows:  scoredRows(0.9, 0.9, 0.9, 0.9, 0.9, 0.9),
			limit: 3,
			want:  3,
		},
		{
			name:  "keeps a minimum so contradictions remain visible",
			rows:  scoredRows(0.9, 0.01),
			limit: 8,
			want:  converseMinNotes,
		},
		{
			name:  "single result",
			rows:  scoredRows(0.9),
			limit: 8,
			want:  1,
		},
		{
			name:  "empty input",
			rows:  nil,
			limit: 8,
			want:  0,
		},
		{
			name:  "zero limit returns nothing",
			rows:  scoredRows(0.9, 0.8),
			limit: 0,
			want:  0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := applyScoreGapCutoff(tc.rows, tc.limit)
			assert.Len(t, got, tc.want)
		})
	}
}

// TestApplyScoreGapCutoff_MissingScoresKeepsAll: keyword-only rows can arrive
// without a score, and dropping them silently would lose valid results.
func TestApplyScoreGapCutoff_MissingScoresKeepsAll(t *testing.T) {
	rows := []model.ThoughtRow{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	assert.Len(t, applyScoreGapCutoff(rows, 8), 3)
}

func TestFilterSeen(t *testing.T) {
	rows := scoredRows(0.9, 0.8, 0.7) // ids a, b, c
	seen := map[string]bool{"b": true}

	got := filterSeen(rows, seen)
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].ID)
	assert.Equal(t, "c", got[1].ID)
}

func TestFilterSeen_EmptyInput(t *testing.T) {
	assert.Nil(t, filterSeen(nil, map[string]bool{}))
}

func TestConverseMaxNotes_FallsBackWhenUnset(t *testing.T) {
	assert.Equal(t, defaultConverseMaxNotes, (&Brain{cfg: &config.Config{}}).converseMaxNotes())
	assert.Equal(t, 3, (&Brain{cfg: &config.Config{ConverseMaxNotes: 3}}).converseMaxNotes())
	assert.Equal(t, defaultConverseMaxNotes, (&Brain{}).converseMaxNotes())
}

// TestConverseMaxRounds_DefaultsToOne pins the behaviour that removed the
// per-round model gate: one round means no gate call at all.
func TestConverseMaxRounds_DefaultsToOne(t *testing.T) {
	assert.Equal(t, 1, (&Brain{cfg: &config.Config{}}).converseMaxRounds())
	assert.Equal(t, 3, (&Brain{cfg: &config.Config{ConverseMaxRounds: 3}}).converseMaxRounds())
	assert.Equal(t, 1, (&Brain{}).converseMaxRounds())
}

// TestConverseSearch_RewriteFailureIsRecoverable asserts a failed rewrite falls
// back to the raw question rather than aborting. Small models frequently return
// chatty or over-long rewrites, and that must not surface as a user error.
func TestConverseSearch_RewriteFailureIsRecoverable(t *testing.T) {
	f := &fakeConverser{rewriteErr: errors.New("model returned garbage")}
	b := &Brain{
		converser: f,
		cfg:       &config.Config{},
		embedder:  failingEmbedder{},
	}

	// Retrieval fails at the embed step, but the important assertion is that
	// execution reached retrieval at all instead of aborting on the rewrite.
	_, err := b.ConverseSearch(context.Background(), "what did I decide?", SearchOpts{}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embed conversational search query",
		"a failed rewrite should fall through to retrieval using the raw question")
}

// TestConverseSearch_CancellationDuringRewritePropagates ensures a user pressing
// stop is not misreported as a model failure.
func TestConverseSearch_CancellationDuringRewritePropagates(t *testing.T) {
	f := &fakeConverser{rewriteErr: context.Canceled}
	b := &Brain{converser: f, cfg: &config.Config{}, embedder: failingEmbedder{}}

	_, err := b.ConverseSearch(context.Background(), "question?", SearchOpts{}, nil, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// failingEmbedder makes the retrieval step fail deterministically without a DB.
type failingEmbedder struct{}

func (failingEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, errors.New("no embedder in test")
}

func (failingEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	return nil, errors.New("no embedder in test")
}

func (failingEmbedder) Dimension() int { return 3 }

// TestFakeConverser_SatisfiesGaterOnlyWhenIntended documents the type assertion
// that gates extra rounds: a provider without SufficiencyGate must not be
// consulted, which is what keeps the default single-round path LLM-free.
func TestConverser_GaterAssertion(t *testing.T) {
	var withGate any = &fakeConverser{}
	_, ok := withGate.(converse.Gater)
	assert.True(t, ok, "fakeConverser should implement Gater")

	var withoutGate any = &noGateConverser{}
	_, ok = withoutGate.(converse.Gater)
	assert.False(t, ok, "a provider without SufficiencyGate must not satisfy Gater")
}

// noGateConverser implements only the required Provider surface.
type noGateConverser struct{}

func (noGateConverser) UnderstandQuery(context.Context, string) (string, error) { return "q", nil }
func (noGateConverser) StreamAnswer(context.Context, string, string, []model.ThoughtRow, func(string) error) (string, error) {
	return "answer", nil
}

// TestConverseSearch_AnswerIsCitationVerified asserts the brain narrows Sources
// to what the answer actually cited and renumbers the markers to match, so
// bracket N in the answer indexes Sources[N-1] for the UI.
func TestVerifyCitationsContract(t *testing.T) {
	notes := []model.ThoughtRow{
		{ID: "n1"}, {ID: "n2"}, {ID: "n3"}, {ID: "n4"},
	}
	answer, sources := converse.VerifyCitations("Only these matter [2] and [4].", notes)

	require.Len(t, sources, 2)
	assert.Equal(t, "n2", sources[0].ID)
	assert.Equal(t, "n4", sources[1].ID)
	assert.Equal(t, "Only these matter [1] and [2].", answer)
}
