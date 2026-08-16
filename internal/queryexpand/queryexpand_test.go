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

func TestParseStreamingResponseExtractsRefinedQuery(t *testing.T) {
	result, err := ParseStreamingResponse("TENT refers to the local transactive energy market engine.\nSEARCH_QUERY: TENT transactive energy market engine")
	require.NoError(t, err)
	assert.True(t, result.UseExpansion)
	assert.Equal(t, "TENT refers to the local transactive energy market engine.", result.Interpretation)
	assert.Equal(t, []string{"TENT transactive energy market engine"}, result.ExpandedTerms)
}

func TestParseStreamingResponseRequiresQueryMarker(t *testing.T) {
	_, err := ParseStreamingResponse("TENT is an energy project.")
	assert.Error(t, err)
}
