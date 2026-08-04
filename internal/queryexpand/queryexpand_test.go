package queryexpand

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseResponseGroundedExpansion(t *testing.T) {
	result, err := ParseResponse(`{"interpretation":"Transactive Energy Node Network","expanded_terms":["TENT","energy node network","TENT"],"confidence":0.94,"use_expansion":true}`)
	require.NoError(t, err)
	assert.True(t, result.UseExpansion)
	assert.Equal(t, []string{"Transactive Energy Node Network", "TENT", "energy node network"}, result.ExpandedTerms)
}

func TestParseResponseRejectsLowConfidenceExpansion(t *testing.T) {
	result, err := ParseResponse(`{"interpretation":"camping tent","expanded_terms":["campfire"],"confidence":0.4,"use_expansion":true}`)
	require.NoError(t, err)
	assert.False(t, result.UseExpansion)
}

func TestParseResponseRejectsInvalidJSON(t *testing.T) {
	_, err := ParseResponse("not json")
	assert.Error(t, err)
}
