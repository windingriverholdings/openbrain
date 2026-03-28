package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultSearchScoreThreshold(t *testing.T) {
	// The default threshold should be 0.15, not 0.35.
	// 0.35 is too aggressive for small corpora.
	cfg := &Config{}
	// When loaded from env with defaults, the threshold should be 0.15
	// For a zero-value config, we test the documented default.
	assert.Equal(t, 0.15, defaultSearchScoreThreshold,
		"default threshold should be 0.15 for small corpora compatibility")
}
