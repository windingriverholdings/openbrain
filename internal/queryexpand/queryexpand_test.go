package queryexpand

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParsePreservesOriginalAndDeduplicates(t *testing.T) {
	got := Parse(`["camping shelter", "TENT", "camping shelter", "trip gear"]`, "tent", 4)
	assert.Equal(t, []string{"tent", "camping shelter", "trip gear"}, got)
}

func TestParseMarkdownFenceAndLimit(t *testing.T) {
	got := Parse("```json\n[\"one\", \"two\", \"three\"]\n```", "original", 3)
	assert.Equal(t, []string{"original", "one", "two"}, got)
}

func TestParseMalformedFallsBackToOriginal(t *testing.T) {
	assert.Equal(t, []string{"original"}, Parse("not json", "original", 6))
}
