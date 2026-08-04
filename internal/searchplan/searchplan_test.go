package searchplan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseValidPlan(t *testing.T) {
	plan, err := Parse(`{"steps":[{"query":"TENT","mode":"hybrid","top_k":12}]}`)
	require.NoError(t, err)
	assert.Equal(t, 12, plan.Steps[0].TopK)
}

func TestParseRejectsUnsafeMode(t *testing.T) {
	_, err := Parse(`{"steps":[{"query":"TENT","mode":"sql"}]}`)
	assert.Error(t, err)
}

func TestParseRejectsTooManySteps(t *testing.T) {
	_, err := Parse(`{"steps":[{"query":"a","mode":"hybrid"},{"query":"b","mode":"hybrid"},{"query":"c","mode":"hybrid"},{"query":"d","mode":"hybrid"}]}`)
	assert.Error(t, err)
}

func TestParseDropsUnknownOptionalThoughtType(t *testing.T) {
	plan, err := Parse(`{"steps":[{"query":"TENT","mode":"hybrid","thought_type":"classification"}]}`)
	require.NoError(t, err)
	assert.Empty(t, plan.Steps[0].ThoughtType)
}
