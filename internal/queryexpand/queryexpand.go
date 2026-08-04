// Package queryexpand expands natural-language searches into retrieval queries.
package queryexpand

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	wrsllm "github.com/windingriverholdings/wrs-llm"

	"github.com/windingriverholdings/openbrain/internal/config"
)

const systemPrompt = "You expand personal knowledge-base search queries. Return only a JSON array of 3 to 6 short search queries. Preserve explicit names, dates, and constraints. Add related concepts and synonyms, but do not invent personal facts. Do not answer the query."

// Expander generates alternate retrieval queries.
type Expander interface {
	Expand(ctx context.Context, query string) ([]string, error)
}

type ollamaExpander struct {
	provider wrsllm.Provider
}

// New creates a local Ollama query expander. The fast extraction model is used
// when configured, because expansion should be a small, low-latency operation.
func New(cfg *config.Config) (Expander, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration is unavailable")
	}
	model := cfg.SearchAssistedModel
	if model == "" {
		model = cfg.ExtractModelFast
	}
	if model == "" {
		model = cfg.ExtractModel
	}
	if model == "" {
		return nil, fmt.Errorf("no local model configured for assisted search")
	}
	return &ollamaExpander{provider: wrsllm.NewOllamaProvider(cfg.OllamaBaseURL, model, 0)}, nil
}

func (e *ollamaExpander) Expand(ctx context.Context, query string) ([]string, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("cannot expand an empty query")
	}
	raw, err := e.provider.Generate(ctx, fmt.Sprintf("Original search query:\n%s\n\nReturn related retrieval queries.", query), systemPrompt)
	if err != nil {
		return nil, fmt.Errorf("local query expansion: %w", err)
	}
	queries := Parse(raw, query, 6)
	if len(queries) == 0 {
		return nil, fmt.Errorf("local query expansion returned no queries")
	}
	slog.Debug("query expansion complete", "query_len", len(query), "expanded_queries", len(queries))
	return queries, nil
}

// Parse extracts a bounded, deduplicated query list and always preserves the
// original query as the first retrieval signal.
func Parse(raw, original string, max int) []string {
	original = strings.TrimSpace(original)
	if max < 1 || original == "" {
		return nil
	}
	text := strings.TrimSpace(raw)
	if strings.HasPrefix(text, "```") {
		var lines []string
		for _, line := range strings.Split(text, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "```") {
				lines = append(lines, line)
			}
		}
		text = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	var values []string
	if err := json.Unmarshal([]byte(text), &values); err != nil {
		start, end := strings.Index(text, "["), strings.LastIndex(text, "]")
		if start >= 0 && end > start {
			_ = json.Unmarshal([]byte(text[start:end+1]), &values)
		}
	}

	result := []string{original}
	seen := map[string]bool{strings.ToLower(original): true}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
		if len(result) == max {
			break
		}
	}
	return result
}
