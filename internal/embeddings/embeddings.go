// Package embeddings defines the Embedder interface and provides implementations
// for generating text embeddings.
package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/craig8/openbrain/internal/config"
)

// Embedder generates vector embeddings from text.
type Embedder interface {
	// Embed returns a vector embedding for a single text string.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch returns vector embeddings for multiple texts.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimension returns the embedding vector dimensionality.
	Dimension() int
}

// OllamaEmbedder generates embeddings via the Ollama HTTP API.
type OllamaEmbedder struct {
	baseURL string
	model   string
	dim     int
	client  *http.Client
}

// NewOllamaEmbedder creates an embedder that calls the Ollama API.
func NewOllamaEmbedder(cfg *config.Config) *OllamaEmbedder {
	return &OllamaEmbedder{
		baseURL: cfg.OllamaBaseURL,
		model:   cfg.EmbeddingModel,
		dim:     cfg.EmbeddingDim,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(ollamaEmbedRequest{Model: e.model, Prompt: text})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed: status %d: %s", resp.StatusCode, respBody)
	}

	var result ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}

	return result.Embedding, nil
}

func (e *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	results := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := e.Embed(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("embed batch item %d: %w", i, err)
		}
		results[i] = vec
	}
	return results, nil
}

func (e *OllamaEmbedder) Dimension() int {
	return e.dim
}
