package searchplan

import (
	"context"
	"fmt"
	"strings"

	wrsllm "github.com/windingriverholdings/wrs-llm"

	"github.com/windingriverholdings/openbrain/internal/config"
)

const plannerSystem = "You are a read-only search planner for a personal knowledge base. Return only valid JSON matching {\"steps\":[{\"query\":\"...\",\"mode\":\"vector|keyword|hybrid\",\"thought_type\":\"...\",\"tags\":[\"...\"],\"include_history\":false,\"top_k\":10}]}. You may create at most three searches. Never return SQL, database commands, writes, or unsupported modes. Use only searches useful for the user's request."

type Planner interface {
	Plan(ctx context.Context, request string) (Plan, error)
}

type ollamaPlanner struct {
	provider wrsllm.Provider
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
	return &ollamaPlanner{provider: wrsllm.NewOllamaProvider(cfg.OllamaBaseURL, modelName, 0)}, nil
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
