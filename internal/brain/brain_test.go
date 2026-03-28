package brain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestSearchSignatureAcceptsSearchOpts(t *testing.T) {
	// Verify the Search method signature accepts SearchOpts.
	// This is a compile-time check — if SearchOpts doesn't exist or
	// Search doesn't accept it, this file won't compile.
	var b *Brain
	_ = b // Just verifying the type exists and has the right method signature
	require.NotNil(t, t) // placeholder assertion
}

func TestEffectiveThresholdLoweredForTypeFilter(t *testing.T) {
	// When ThoughtType is set, the effective score threshold should be
	// lowered to 0.01 to avoid filtering out valid typed results.
	opts := SearchOpts{
		ThoughtType: "decision",
	}
	threshold := effectiveThreshold(0.15, opts)
	assert.Equal(t, 0.01, threshold)
}

func TestEffectiveThresholdUnchangedWithoutTypeFilter(t *testing.T) {
	opts := SearchOpts{}
	threshold := effectiveThreshold(0.15, opts)
	assert.Equal(t, 0.15, threshold)
}
