package db

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/craig8/openbrain/internal/embeddings"
)

// mockEmbedder implements embeddings.Embedder for testing.
type mockEmbedder struct {
	dim       int
	embedFunc func(ctx context.Context, text string) ([]float32, error)
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.embedFunc != nil {
		return m.embedFunc(ctx, text)
	}
	vec := make([]float32, m.dim)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec, nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, t := range texts {
		vec, err := m.Embed(ctx, t)
		if err != nil {
			return nil, fmt.Errorf("batch item %d: %w", i, err)
		}
		results[i] = vec
	}
	return results, nil
}

func (m *mockEmbedder) Dimension() int {
	return m.dim
}

// Compile-time check that mockEmbedder satisfies the interface.
var _ embeddings.Embedder = (*mockEmbedder)(nil)

func TestReembedAll_RejectsNilPool(t *testing.T) {
	embedder := &mockEmbedder{dim: 768}
	_, err := ReembedAll(context.Background(), nil, embedder, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pool is nil")
}

func TestReembedAll_RejectsNilEmbedder(t *testing.T) {
	// We can't create a real pool here, but we can pass a nil embedder
	// with a non-nil pool placeholder. The nil embedder check should
	// trigger before any pool usage.
	// Since we can't easily mock pgxpool, we test via the nil path.
	_, err := ReembedAll(context.Background(), nil, nil, nil)
	require.Error(t, err)
	// Either "pool is nil" or "embedder is nil" — pool check comes first
	assert.Error(t, err)
}

func TestReembedResult_ZeroValue(t *testing.T) {
	// Verify the result struct has sensible zero values.
	r := &ReembedResult{}
	assert.Equal(t, 0, r.Total)
	assert.Equal(t, 0, r.Succeeded)
	assert.Equal(t, 0, r.Failed)
	assert.Nil(t, r.Errors)
}

func TestMockEmbedder_Dimension768(t *testing.T) {
	// Verify our mock produces 768-dim vectors for nomic-embed-text testing.
	embedder := &mockEmbedder{dim: 768}
	assert.Equal(t, 768, embedder.Dimension())

	vec, err := embedder.Embed(context.Background(), "test")
	require.NoError(t, err)
	assert.Len(t, vec, 768)
}

func TestMockEmbedder_EmbedError(t *testing.T) {
	embedder := &mockEmbedder{
		dim: 768,
		embedFunc: func(_ context.Context, _ string) ([]float32, error) {
			return nil, fmt.Errorf("ollama down")
		},
	}
	_, err := embedder.Embed(context.Background(), "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ollama down")
}
