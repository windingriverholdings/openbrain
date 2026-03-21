package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// OllamaProvider calls the Ollama HTTP API for text generation.
type OllamaProvider struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewOllamaProvider creates an Ollama LLM provider.
func NewOllamaProvider(baseURL, model string) *OllamaProvider {
	return &OllamaProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

type ollamaChatRequest struct {
	Model    string            `json:"model"`
	Messages []ollamaChatMsg   `json:"messages"`
	Stream   bool              `json:"stream"`
}

type ollamaChatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatResponse struct {
	Message ollamaChatMsg `json:"message"`
}

func (p *OllamaProvider) Generate(ctx context.Context, prompt, system string) (string, error) {
	var messages []ollamaChatMsg
	if system != "" {
		messages = append(messages, ollamaChatMsg{Role: "system", Content: system})
	}
	messages = append(messages, ollamaChatMsg{Role: "user", Content: prompt})

	body, err := json.Marshal(ollamaChatRequest{Model: p.model, Messages: messages, Stream: false})
	if err != nil {
		return "", fmt.Errorf("marshal ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama: status %d: %s", resp.StatusCode, respBody)
	}

	var result ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}

	slog.Info("ollama response", "model", p.model, "prompt_len", len(prompt), "response_len", len(result.Message.Content))
	return result.Message.Content, nil
}
