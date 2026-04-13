package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddingModelDefault_NomicEmbedText(t *testing.T) {
	// OB-024: Default embedding model should be nomic-embed-text (768d).
	// Unset any env overrides so we get the struct tag default.
	t.Setenv("OPENBRAIN_EMBEDDING_MODEL", "")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "nomic-embed-text", cfg.EmbeddingModel,
		"default embedding model should be nomic-embed-text")
}

func TestEmbeddingDimDefault_768(t *testing.T) {
	// OB-024: Default embedding dimension should be 768 for nomic-embed-text.
	t.Setenv("OPENBRAIN_EMBEDDING_DIM", "")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 768, cfg.EmbeddingDim,
		"default embedding dimension should be 768")
}

func TestEmbeddingModelOverrideFromEnv(t *testing.T) {
	// Verify that env override still works after changing defaults.
	t.Setenv("OPENBRAIN_EMBEDDING_MODEL", "custom-model")
	t.Setenv("OPENBRAIN_EMBEDDING_DIM", "512")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "custom-model", cfg.EmbeddingModel)
	assert.Equal(t, 512, cfg.EmbeddingDim)
}
