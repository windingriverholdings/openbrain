package db

import (
	"context"
	"errors"
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

// mockReembedDB implements ReembedDB for testing the re-embed loop.
type mockReembedDB struct {
	thoughts []reembedRow
	fetchErr error
	updates  []mockUpdate
	updateFn func(id string) error
}

type mockUpdate struct {
	ID     string
	VecLen int
}

func (m *mockReembedDB) FetchNullEmbeddings(ctx context.Context, limit, offset int) ([]reembedRow, error) {
	if m.fetchErr != nil {
		return nil, m.fetchErr
	}
	if offset >= len(m.thoughts) {
		return nil, nil
	}
	end := offset + limit
	if end > len(m.thoughts) {
		end = len(m.thoughts)
	}
	return m.thoughts[offset:end], nil
}

func (m *mockReembedDB) UpdateEmbedding(ctx context.Context, id string, vec []float32) error {
	if m.updateFn != nil {
		return m.updateFn(id)
	}
	m.updates = append(m.updates, mockUpdate{ID: id, VecLen: len(vec)})
	return nil
}

var _ ReembedDB = (*mockReembedDB)(nil)

// --- Nil parameter validation ---

func TestReembedAll_RejectsNilPool(t *testing.T) {
	embedder := &mockEmbedder{dim: 768}
	_, err := ReembedAll(context.Background(), nil, embedder, 768, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db is nil")
}

func TestReembedAll_RejectsNilEmbedder(t *testing.T) {
	db := &mockReembedDB{}
	_, err := ReembedAll(context.Background(), db, nil, 768, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedder is nil")
}

func TestReembedAll_RejectsNilEmbedder_WithNonNilDB(t *testing.T) {
	// Finding #10: Test nil embedder with non-nil DB separately to ensure
	// the embedder check fires (not masked by pool-nil check).
	db := &mockReembedDB{thoughts: []reembedRow{{ID: "abc", Content: "x"}}}
	_, err := ReembedAll(context.Background(), db, nil, 768, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedder is nil")
}

func TestReembedResult_ZeroValue(t *testing.T) {
	r := &ReembedResult{}
	assert.Equal(t, 0, r.Total)
	assert.Equal(t, 0, r.Succeeded)
	assert.Equal(t, 0, r.Failed)
	assert.Nil(t, r.Errors)
}

func TestMockEmbedder_Dimension768(t *testing.T) {
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

// --- Happy path: all thoughts embedded successfully ---

func TestReembedAll_HappyPath(t *testing.T) {
	thoughts := []reembedRow{
		{ID: "aaaa-1111-2222-3333", Content: "thought one"},
		{ID: "bbbb-4444-5555-6666", Content: "thought two"},
		{ID: "cccc-7777-8888-9999", Content: "thought three"},
	}
	db := &mockReembedDB{thoughts: thoughts}
	embedder := &mockEmbedder{dim: 768}

	result, err := ReembedAll(context.Background(), db, embedder, 768, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, result.Total)
	assert.Equal(t, 3, result.Succeeded)
	assert.Equal(t, 0, result.Failed)
	assert.Empty(t, result.Errors)
	assert.Len(t, db.updates, 3)
}

func TestReembedAll_EmptySet(t *testing.T) {
	db := &mockReembedDB{thoughts: nil}
	embedder := &mockEmbedder{dim: 768}

	result, err := ReembedAll(context.Background(), db, embedder, 768, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Total)
	assert.Equal(t, 0, result.Succeeded)
}

// --- Partial failure: some embeds fail, loop continues ---

func TestReembedAll_PartialFailure(t *testing.T) {
	thoughts := []reembedRow{
		{ID: "aaaa-1111", Content: "good one"},
		{ID: "bbbb-2222", Content: "bad one"},
		{ID: "cccc-3333", Content: "good two"},
	}
	db := &mockReembedDB{thoughts: thoughts}
	callCount := 0
	embedder := &mockEmbedder{
		dim: 768,
		embedFunc: func(_ context.Context, _ string) ([]float32, error) {
			callCount++
			if callCount == 2 {
				return nil, fmt.Errorf("embed failed")
			}
			return make([]float32, 768), nil
		},
	}

	result, err := ReembedAll(context.Background(), db, embedder, 768, nil)
	require.NoError(t, err) // partial failure is not a top-level error
	assert.Equal(t, 3, result.Total)
	assert.Equal(t, 2, result.Succeeded)
	assert.Equal(t, 1, result.Failed)
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0], "bbbb-2222") // full ID, not truncated
}

// --- Circuit breaker: consecutive failures trigger early abort ---

func TestReembedAll_CircuitBreaker(t *testing.T) {
	// Create more thoughts than the circuit breaker threshold
	thoughts := make([]reembedRow, 10)
	for i := range thoughts {
		thoughts[i] = reembedRow{ID: fmt.Sprintf("id-%d", i), Content: fmt.Sprintf("content %d", i)}
	}
	db := &mockReembedDB{thoughts: thoughts}
	embedder := &mockEmbedder{
		dim: 768,
		embedFunc: func(_ context.Context, _ string) ([]float32, error) {
			return nil, fmt.Errorf("ollama down")
		},
	}

	result, err := ReembedAll(context.Background(), db, embedder, 768, nil)
	// Circuit breaker should trigger an error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circuit breaker")
	// Should have stopped before processing all 10
	assert.Less(t, result.Failed, 10)
	assert.Equal(t, CircuitBreakerThreshold, result.Failed)
}

// --- Context cancellation stops the loop ---

func TestReembedAll_ContextCancellation(t *testing.T) {
	thoughts := make([]reembedRow, 10)
	for i := range thoughts {
		thoughts[i] = reembedRow{ID: fmt.Sprintf("id-%d", i), Content: fmt.Sprintf("content %d", i)}
	}
	db := &mockReembedDB{thoughts: thoughts}

	ctx, cancel := context.WithCancel(context.Background())
	callCount := 0
	embedder := &mockEmbedder{
		dim: 768,
		embedFunc: func(_ context.Context, _ string) ([]float32, error) {
			callCount++
			if callCount == 3 {
				cancel()
			}
			return make([]float32, 768), nil
		},
	}

	result, err := ReembedAll(ctx, db, embedder, 768, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context")
	// Should have partial results
	assert.Greater(t, result.Succeeded, 0)
	assert.Less(t, result.Succeeded, 10)
}

// --- Dimension validation ---

func TestReembedAll_DimensionMismatch(t *testing.T) {
	thoughts := []reembedRow{
		{ID: "aaaa-1111", Content: "good one"},
		{ID: "bbbb-2222", Content: "wrong dim"},
	}
	db := &mockReembedDB{thoughts: thoughts}
	callCount := 0
	embedder := &mockEmbedder{
		dim: 768,
		embedFunc: func(_ context.Context, _ string) ([]float32, error) {
			callCount++
			if callCount == 2 {
				// Return wrong dimension
				return make([]float32, 384), nil
			}
			return make([]float32, 768), nil
		},
	}

	result, err := ReembedAll(context.Background(), db, embedder, 768, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Total)
	assert.Equal(t, 1, result.Succeeded)
	assert.Equal(t, 1, result.Failed)
	assert.Contains(t, result.Errors[0], "dimension mismatch")
}

// --- Progress callback semantics ---

func TestReembedAll_ProgressCallback(t *testing.T) {
	thoughts := []reembedRow{
		{ID: "aaaa", Content: "one"},
		{ID: "bbbb", Content: "two"},
		{ID: "cccc", Content: "three"},
	}
	db := &mockReembedDB{thoughts: thoughts}
	embedder := &mockEmbedder{dim: 768}

	var callbacks []struct{ processed, total int }
	progressFn := func(processed, total int) {
		callbacks = append(callbacks, struct{ processed, total int }{processed, total})
	}

	result, err := ReembedAll(context.Background(), db, embedder, 768, progressFn)
	require.NoError(t, err)
	assert.Equal(t, 3, result.Succeeded)

	// Progress should be called for each thought processed
	assert.Len(t, callbacks, 3)
	// Each call should have the total as 3
	for _, cb := range callbacks {
		assert.Equal(t, 3, cb.total)
	}
	// Processed should increment
	assert.Equal(t, 1, callbacks[0].processed)
	assert.Equal(t, 2, callbacks[1].processed)
	assert.Equal(t, 3, callbacks[2].processed)
}

// --- Batching: processes in batches ---

func TestReembedAll_BatchesCorrectly(t *testing.T) {
	// Create more thoughts than a single batch
	thoughts := make([]reembedRow, 250)
	for i := range thoughts {
		thoughts[i] = reembedRow{ID: fmt.Sprintf("id-%03d", i), Content: fmt.Sprintf("content %d", i)}
	}
	db := &mockReembedDB{thoughts: thoughts}
	embedder := &mockEmbedder{dim: 768}

	result, err := ReembedAll(context.Background(), db, embedder, 768, nil)
	require.NoError(t, err)
	assert.Equal(t, 250, result.Total)
	assert.Equal(t, 250, result.Succeeded)
	assert.Equal(t, 0, result.Failed)
	assert.Len(t, db.updates, 250)
}

// --- Fetch error ---

func TestReembedAll_FetchError(t *testing.T) {
	db := &mockReembedDB{fetchErr: fmt.Errorf("connection refused")}
	embedder := &mockEmbedder{dim: 768}

	_, err := ReembedAll(context.Background(), db, embedder, 768, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

// --- Update failure ---

func TestReembedAll_UpdateFailure(t *testing.T) {
	thoughts := []reembedRow{
		{ID: "aaaa", Content: "one"},
		{ID: "bbbb", Content: "two"},
	}
	db := &mockReembedDB{
		thoughts: thoughts,
		updateFn: func(id string) error {
			if id == "bbbb" {
				return fmt.Errorf("update failed")
			}
			return nil
		},
	}
	embedder := &mockEmbedder{dim: 768}

	result, err := ReembedAll(context.Background(), db, embedder, 768, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Succeeded)
	assert.Equal(t, 1, result.Failed)
	assert.Contains(t, result.Errors[0], "update failed")
}

// --- CLI reembed wiring ---

func TestCmdReembed_ReturnsErrorOnAllFailed(t *testing.T) {
	// Finding #1: cmdReembed must return non-zero when failures occur.
	// We test the logic by creating a result with all failures.
	result := &ReembedResult{Total: 5, Succeeded: 0, Failed: 5}
	err := checkReembedResult(result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all 5 thoughts failed")
}

func TestCmdReembed_ReturnsErrorOnPartialFailure(t *testing.T) {
	result := &ReembedResult{Total: 5, Succeeded: 3, Failed: 2}
	err := checkReembedResult(result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 of 5")
}

func TestCmdReembed_NoErrorOnFullSuccess(t *testing.T) {
	result := &ReembedResult{Total: 5, Succeeded: 5, Failed: 0}
	err := checkReembedResult(result)
	require.NoError(t, err)
}

func TestCmdReembed_NoErrorOnZeroThoughts(t *testing.T) {
	result := &ReembedResult{Total: 0, Succeeded: 0, Failed: 0}
	err := checkReembedResult(result)
	require.NoError(t, err)
}

// --- Circuit breaker resets on success ---

func TestReembedAll_CircuitBreakerResetsOnSuccess(t *testing.T) {
	thoughts := make([]reembedRow, 10)
	for i := range thoughts {
		thoughts[i] = reembedRow{ID: fmt.Sprintf("id-%d", i), Content: fmt.Sprintf("content %d", i)}
	}
	db := &mockReembedDB{thoughts: thoughts}
	callCount := 0
	embedder := &mockEmbedder{
		dim: 768,
		embedFunc: func(_ context.Context, _ string) ([]float32, error) {
			callCount++
			// Fail every other one but never hit threshold consecutive
			if callCount%2 == 0 {
				return nil, fmt.Errorf("intermittent failure")
			}
			return make([]float32, 768), nil
		},
	}

	result, err := ReembedAll(context.Background(), db, embedder, 768, nil)
	// No circuit breaker should fire — failures are not consecutive
	if errors.Is(err, context.Canceled) {
		t.Skip("context cancelled unexpectedly")
	}
	// With alternating success/fail, no circuit breaker
	if err != nil {
		assert.NotContains(t, err.Error(), "circuit breaker")
	}
	assert.Equal(t, 5, result.Succeeded)
	assert.Equal(t, 5, result.Failed)
}
