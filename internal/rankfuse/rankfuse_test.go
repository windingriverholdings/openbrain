package rankfuse

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/windingriverholdings/openbrain/internal/model"
)

// TestFuseRRF_AgreementBeatsSingleAxis is the core contract: a row that two
// independent retrieval strategies both rank highly must outrank a row that
// only one strategy found, even when the single-axis row was that strategy's
// top hit.
func TestFuseRRF_AgreementBeatsSingleAxis(t *testing.T) {
	lexical := []model.ThoughtRow{{ID: "solo-lexical"}, {ID: "both"}}
	semantic := []model.ThoughtRow{{ID: "solo-semantic"}, {ID: "both"}}

	got := FuseRRF(DefaultK, lexical, semantic)

	require.Len(t, got, 3)
	assert.Equal(t, "both", got[0].ID,
		"a row ranked second by both axes must beat rows ranked first by only one axis")
}

// TestFuseRRF_IgnoresSourceScoreMagnitude pins the reason this fusion exists.
// Full-text rank and cosine similarity live on different numeric scales, so
// summing them lets the larger-valued system dominate. Fusion reads positions
// only, so rescaling one axis's scores by any factor must not change the order.
func TestFuseRRF_IgnoresSourceScoreMagnitude(t *testing.T) {
	// The lexical axis ranks "codename" first but with a tiny score, exactly
	// how ts_rank behaves for a rare proper noun.
	lexicalSmall := []model.ThoughtRow{
		{ID: "codename", Score: ptr(0.04)},
		{ID: "other", Score: ptr(0.02)},
	}
	// The semantic axis ranks "other" first with a much larger score.
	semanticLarge := []model.ThoughtRow{
		{ID: "other", Score: ptr(0.81)},
		{ID: "codename", Score: ptr(0.78)},
	}

	base := ids(FuseRRF(DefaultK, lexicalSmall, semanticLarge))

	// Rescale the lexical scores by 1000x. Positions are unchanged, so the
	// fused order must be identical.
	lexicalHuge := []model.ThoughtRow{
		{ID: "codename", Score: ptr(40)},
		{ID: "other", Score: ptr(20)},
	}
	rescaled := ids(FuseRRF(DefaultK, lexicalHuge, semanticLarge))

	assert.Equal(t, base, rescaled,
		"fused order must depend only on rank position, never on score magnitude")
}

// TestFuseRRF_ReplacesIncomparableScores asserts the returned Score is the
// fused value, not a leftover per-axis score. Surfacing a raw per-axis score
// after fusion would misrepresent the ranking that produced it.
func TestFuseRRF_ReplacesIncomparableScores(t *testing.T) {
	got := FuseRRF(DefaultK, []model.ThoughtRow{{ID: "a", Score: ptr(0.9)}})

	require.Len(t, got, 1)
	require.NotNil(t, got[0].Score)
	assert.InDelta(t, 1.0/61.0, *got[0].Score, 1e-9)
}

// TestFuseRRF_SingleAxisPreservesThatAxisOrder guarantees fusion is a no-op on
// ordering when only one axis produced results, which is the degraded path when
// the other axis errors or matches nothing.
func TestFuseRRF_SingleAxisPreservesThatAxisOrder(t *testing.T) {
	got := FuseRRF(DefaultK, []model.ThoughtRow{{ID: "first"}, {ID: "second"}, {ID: "third"}})
	assert.Equal(t, []string{"first", "second", "third"}, ids(got))
}

// TestFuseRRF_ToleratesEmptyAndNilLists covers the failure-isolation contract:
// an axis that matched nothing must not corrupt or drop the surviving axis.
func TestFuseRRF_ToleratesEmptyAndNilLists(t *testing.T) {
	got := FuseRRF(DefaultK, nil, []model.ThoughtRow{{ID: "kept"}}, []model.ThoughtRow{})
	assert.Equal(t, []string{"kept"}, ids(got))

	assert.Empty(t, FuseRRF(DefaultK), "no lists at all must yield no rows, not panic")
	assert.Empty(t, FuseRRF(DefaultK, nil, nil))
}

// TestFuseRRF_DeduplicatesWithinOneList prevents a row repeated inside a single
// axis from being credited twice, which would let one axis outvote the others.
func TestFuseRRF_DeduplicatesWithinOneList(t *testing.T) {
	dupes := []model.ThoughtRow{{ID: "a"}, {ID: "a"}, {ID: "a"}}
	single := []model.ThoughtRow{{ID: "b"}}

	got := FuseRRF(DefaultK, dupes, single)

	require.Len(t, got, 2, "a repeated row must appear once")
	assert.Equal(t, "a", got[0].ID)
	require.NotNil(t, got[0].Score)
	assert.InDelta(t, 1.0/61.0, *got[0].Score, 1e-9,
		"a row repeated within one axis must be credited once, at its best rank")
}

// TestFuseRRF_TieBreaksDeterministically keeps result ordering stable across
// runs when scores collide, so the UI does not reshuffle identical searches.
func TestFuseRRF_TieBreaksDeterministically(t *testing.T) {
	first := ids(FuseRRF(DefaultK, []model.ThoughtRow{{ID: "zeta"}}, []model.ThoughtRow{{ID: "alpha"}}))
	for i := 0; i < 20; i++ {
		assert.Equal(t, first,
			ids(FuseRRF(DefaultK, []model.ThoughtRow{{ID: "zeta"}}, []model.ThoughtRow{{ID: "alpha"}})))
	}
	assert.Equal(t, []string{"alpha", "zeta"}, first, "equal scores must break by id")
}

// TestFuseRRF_SkipsRowsWithoutID guards against a malformed row silently
// colliding with other rows under the empty-string key.
func TestFuseRRF_SkipsRowsWithoutID(t *testing.T) {
	got := FuseRRF(DefaultK, []model.ThoughtRow{{ID: ""}, {ID: "real"}})
	assert.Equal(t, []string{"real"}, ids(got))
}

// TestFuseRRF_InvalidKFallsBackToDefault ensures a misconfigured damping
// constant cannot produce a divide-by-zero or negative weight.
func TestFuseRRF_InvalidKFallsBackToDefault(t *testing.T) {
	for _, k := range []int{0, -1, -100} {
		got := FuseRRF(k, []model.ThoughtRow{{ID: "a"}})
		require.Len(t, got, 1)
		require.NotNil(t, got[0].Score)
		assert.InDelta(t, 1.0/61.0, *got[0].Score, 1e-9)
	}
}

// TestFuseRRF_LargerKFlattensContributions documents the tuning knob: a bigger
// damping constant narrows the gap between rank positions.
func TestFuseRRF_LargerKFlattensContributions(t *testing.T) {
	list := []model.ThoughtRow{{ID: "first"}, {ID: "second"}}

	tight := FuseRRF(1, list)
	flat := FuseRRF(1000, list)

	tightGap := *tight[0].Score - *tight[1].Score
	flatGap := *flat[0].Score - *flat[1].Score
	assert.Greater(t, tightGap, flatGap,
		"a smaller k must concentrate more weight at the top of each list")
}

// TestFuseRRF_DoesNotMutateInput protects callers that reuse the per-axis
// slices, for example to report how many rows each axis contributed.
func TestFuseRRF_DoesNotMutateInput(t *testing.T) {
	original := 0.42
	list := []model.ThoughtRow{{ID: "a", Score: &original}}

	FuseRRF(DefaultK, list)

	require.NotNil(t, list[0].Score)
	assert.InDelta(t, 0.42, *list[0].Score, 1e-9,
		"fusion must not overwrite the caller's per-axis scores")
}

func ids(rows []model.ThoughtRow) []string {
	out := make([]string, len(rows))
	for i := range rows {
		out[i] = rows[i].ID
	}
	return out
}

func ptr(v float64) *float64 { return &v }
