package searchplan

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	wrsllm "github.com/windingriverholdings/wrs-llm"

	"github.com/windingriverholdings/openbrain/internal/config"
)

const plannerSystem = "You are a read-only search planner for a personal knowledge base. Return only valid JSON matching {\"steps\":[{\"query\":\"...\",\"mode\":\"vector|keyword|hybrid\",\"thought_type\":\"...\",\"tags\":[\"...\"],\"include_history\":false,\"top_k\":10}]}. You may create at most three searches. Guidelines: 1) Use 'hybrid' or 'vector' for natural-language concepts, questions, and topics. Use 'keyword' ONLY for literal identifiers, rare tags, or exact-match terms. 2) Keep queries extremely concise (1-3 words). Never use long conversational questions. 3) Never return SQL, database commands, writes, or unsupported modes."

type Planner interface {
	Plan(ctx context.Context, request string) (Plan, error)
}

// StreamPlanner exposes the planner's model output as it arrives from Ollama.
// The final output remains subject to the same strict Plan validation.
type StreamPlanner interface {
	Planner
	StreamPlan(ctx context.Context, request string, onChunk func(string) error) (Plan, error)
}

type ollamaPlanner struct {
	provider wrsllm.Provider
	model    string
	baseURL  string
}

func NewPlanner(cfg *config.Config) (Planner, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration is unavailable")
	}
	modelName := cfg.SearchAssistedModel
	if modelName == "" {
		modelName = cfg.ExtractModelFast
	}
	if modelName == "" {
		modelName = cfg.ExtractModel
	}
	if modelName == "" {
		return nil, fmt.Errorf("no local model configured for AI Search")
	}
	return &ollamaPlanner{
		provider: wrsllm.NewOllamaProvider(cfg.OllamaBaseURL, modelName, 0),
		model:    modelName,
		baseURL:  cfg.OllamaBaseURL,
	}, nil
}

func (p *ollamaPlanner) Plan(ctx context.Context, request string) (Plan, error) {
	if strings.TrimSpace(request) == "" {
		return Plan{}, fmt.Errorf("empty search request")
	}
	prompt := fmt.Sprintf("User search request:\n%s\n\nCreate the smallest useful set of independent read-only searches. Preserve important exact terms and expand only when the request clearly calls for it.", strings.TrimSpace(request))
	raw, err := p.provider.Generate(ctx, prompt, plannerSystem)
	if err != nil {
		return Plan{}, fmt.Errorf("AI Search planner: %w", err)
	}
	return Parse(raw)
}

func (p *ollamaPlanner) StreamPlan(ctx context.Context, request string, onChunk func(string) error) (Plan, error) {
	if strings.TrimSpace(request) == "" {
		return Plan{}, fmt.Errorf("empty search request")
	}
	prompt := fmt.Sprintf("User search request:\n%s\n\nCreate the smallest useful set of independent read-only searches. Preserve important exact terms and expand only when the request clearly calls for it.", strings.TrimSpace(request))
	reqPayload := struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}{Model: p.model, Stream: true}
	reqPayload.Messages = []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{
		{Role: "system", Content: plannerSystem},
		{Role: "user", Content: prompt},
	}

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(reqPayload); err != nil {
		return Plan{}, err
	}
	baseURL := strings.TrimSuffix(p.baseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/chat", &body)
	if err != nil {
		return Plan{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Plan{}, fmt.Errorf("AI Search planner: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		return Plan{}, fmt.Errorf("AI Search planner: ollama returned status %d: %s", resp.StatusCode, responseBody)
	}

	var raw strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var event struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return Plan{}, fmt.Errorf("AI Search planner: decode stream: %w", err)
		}
		if event.Message.Content != "" {
			raw.WriteString(event.Message.Content)
			if err := onChunk(event.Message.Content); err != nil {
				return Plan{}, err
			}
		}
		if event.Done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return Plan{}, fmt.Errorf("AI Search planner: read stream: %w", err)
	}
	return Parse(raw.String())
}
