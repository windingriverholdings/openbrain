package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/craig8/openbrain/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEmbedder(serverURL string) *OllamaEmbedder {
	cfg := &config.Config{
		OllamaBaseURL:  serverURL,
		EmbeddingModel: "test-model",
		EmbeddingDim:   384,
	}
	return NewOllamaEmbedder(cfg)
}

func TestEmbed_ReturnsErrorOnEmptyEmbedding(t *testing.T) {
	// Simulate Ollama returning a 200 OK but with an empty embedding vector.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaEmbedResponse{Embedding: []float32{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	embedder := newTestEmbedder(srv.URL)
	_, err := embedder.Embed(context.Background(), "test text")

	require.Error(t, err, "Embed must return an error when Ollama returns an empty embedding")
	assert.Contains(t, err.Error(), "empty embedding")
}

func TestEmbed_ReturnsErrorOnNilEmbedding(t *testing.T) {
	// Simulate Ollama returning a response with a nil/missing embedding field.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"embedding": null}`))
	}))
	defer srv.Close()

	embedder := newTestEmbedder(srv.URL)
	_, err := embedder.Embed(context.Background(), "test text")

	require.Error(t, err, "Embed must return an error when Ollama returns a nil embedding")
	assert.Contains(t, err.Error(), "empty embedding")
}

func TestEmbed_SucceedsWithValidEmbedding(t *testing.T) {
	// Sanity check: valid embeddings should pass through without error.
	expected := []float32{0.1, 0.2, 0.3}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaEmbedResponse{Embedding: expected}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	embedder := newTestEmbedder(srv.URL)
	result, err := embedder.Embed(context.Background(), "test text")

	require.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestEmbed_TruncatesLargeErrorBody(t *testing.T) {
	// When Ollama returns a non-200 status with a body larger than 512 bytes,
	// the error message must not include more than 512 bytes of the response body.
	largeBody := make([]byte, 2048)
	for i := range largeBody {
		largeBody[i] = 'X'
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(largeBody)
	}))
	defer srv.Close()

	embedder := newTestEmbedder(srv.URL)
	_, err := embedder.Embed(context.Background(), "test text")

	require.Error(t, err, "Embed must return an error on non-200 status")
	// The error message should contain at most 512 bytes of the response body,
	// not the full 2048 bytes.
	errMsg := err.Error()
	// Count how many 'X' characters are in the error — should be at most 512
	xCount := 0
	for _, c := range errMsg {
		if c == 'X' {
			xCount++
		}
	}
	assert.LessOrEqual(t, xCount, 512, "Error body should be truncated to at most 512 bytes")
	assert.Greater(t, xCount, 0, "Error should contain some of the response body")
}

func TestEmbedBatch_ReturnsErrorOnEmptyEmbedding(t *testing.T) {
	// If any embedding in a batch comes back empty, the batch should fail.
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var resp ollamaEmbedResponse
		if callCount == 1 {
			resp = ollamaEmbedResponse{Embedding: []float32{0.1, 0.2}}
		} else {
			resp = ollamaEmbedResponse{Embedding: []float32{}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	embedder := newTestEmbedder(srv.URL)
	_, err := embedder.EmbedBatch(context.Background(), []string{"good", "bad"})

	require.Error(t, err, "EmbedBatch must fail when any embedding is empty")
	assert.Contains(t, err.Error(), "empty embedding")
}
