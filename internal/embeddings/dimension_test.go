package embeddings

import (
	"testing"

	"github.com/craig8/openbrain/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestNewOllamaEmbedder_NomicDefaults(t *testing.T) {
	// OB-024: With default config, embedder should use nomic-embed-text at 768 dims.
	cfg := &config.Config{
		OllamaBaseURL:  "http://localhost:11434",
		EmbeddingModel: "nomic-embed-text",
		EmbeddingDim:   768,
	}
	embedder := NewOllamaEmbedder(cfg)

	assert.Equal(t, 768, embedder.Dimension(),
		"embedder dimension should be 768 for nomic-embed-text")
	assert.Equal(t, "nomic-embed-text", embedder.model,
		"embedder model should be nomic-embed-text")
}

func TestNewOllamaEmbedder_DimensionMatchesConfig(t *testing.T) {
	tests := []struct {
		name  string
		model string
		dim   int
	}{
		{"nomic-embed-text", "nomic-embed-text", 768},
		{"all-minilm legacy", "all-minilm", 384},
		{"custom model", "custom-model", 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				OllamaBaseURL:  "http://localhost:11434",
				EmbeddingModel: tt.model,
				EmbeddingDim:   tt.dim,
			}
			embedder := NewOllamaEmbedder(cfg)
			assert.Equal(t, tt.dim, embedder.Dimension())
		})
	}
}
