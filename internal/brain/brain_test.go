package brain

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
