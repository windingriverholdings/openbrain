// Package queryexpand performs local-model, note-grounded search expansion.
package queryexpand

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	wrsllm "github.com/windingriverholdings/wrs-llm"

	"github.com/windingriverholdings/openbrain/internal/config"
	"github.com/windingriverholdings/openbrain/internal/model"
)

const systemPrompt = "You interpret personal knowledge-base search terms only from the supplied notes. Do not use common-world associations that are not supported by the notes. Return only valid JSON. If the notes do not establish a specific meaning, return an empty expansion."

const maxExpandedTerms = 3

// Result is the model's grounded interpretation of a search query.
type Result struct {
	Interpretation string   `json:"interpretation"`
	ExpandedTerms  []string `json:"expanded_terms"`
	Confidence     float64  `json:"confidence"`
	UseExpansion   bool     `json:"use_expansion"`
}

// Provider is the model seam used by assisted search.
type Provider interface {
	Expand(ctx context.Context, query string, notes []model.ThoughtRow) (Result, error)
}

type ollamaProvider struct {
	provider wrsllm.Provider
	model    string
}

// New creates a provider using the configured assisted-search model fallback.
func New(cfg *config.Config) (Provider, error) {
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
		return nil, fmt.Errorf("no local model configured for search expansion")
	}
	return &ollamaProvider{
		provider: wrsllm.NewOllamaProvider(cfg.OllamaBaseURL, modelName, 0),
		model:    modelName,
	}, nil
}

func (p *ollamaProvider) Expand(ctx context.Context, query string, notes []model.ThoughtRow) (Result, error) {
	if strings.TrimSpace(query) == "" {
		return Result{}, fmt.Errorf("cannot expand an empty query")
	}
	if len(notes) == 0 {
		return Result{}, nil
	}
	raw, err := p.provider.Generate(ctx, FormatPrompt(query, notes), systemPrompt)
	if err != nil {
		return Result{}, fmt.Errorf("local search expansion: %w", err)
	}
	result, err := ParseResponse(raw)
	if err != nil {
		return Result{}, fmt.Errorf("parse local search expansion: %w", err)
	}
	return result, nil
}

// ParseResponse parses a model response, tolerating surrounding commentary.
func ParseResponse(raw string) (Result, error) {
	text := strings.TrimSpace(raw)
	if strings.HasPrefix(text, "```") {
		lines := make([]string, 0)
		for _, line := range strings.Split(text, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "```") {
				lines = append(lines, line)
			}
		}
		text = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	var result Result
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
		if start < 0 || end <= start || json.Unmarshal([]byte(text[start:end+1]), &result) != nil {
			return Result{}, fmt.Errorf("invalid JSON response")
		}
	}
	result.Interpretation = strings.TrimSpace(result.Interpretation)
	if result.Confidence < 0 {
		result.Confidence = 0
	}
	if result.Confidence > 1 {
		result.Confidence = 1
	}
	terms := make([]string, 0, maxExpandedTerms)
	seen := make(map[string]bool)
	for _, term := range append([]string{result.Interpretation}, result.ExpandedTerms...) {
		term = strings.TrimSpace(term)
		key := strings.ToLower(term)
		if term == "" || seen[key] || len(terms) >= maxExpandedTerms {
			continue
		}
		seen[key] = true
		terms = append(terms, term)
	}
	result.ExpandedTerms = terms
	if result.Confidence < 0.6 || len(result.ExpandedTerms) == 0 {
		result.UseExpansion = false
	}
	return result, nil
}

// FormatPrompt creates a bounded, grounded expansion prompt.
func FormatPrompt(query string, notes []model.ThoughtRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Search query: %s\n\nCandidate notes from this personal brain:\n", strings.TrimSpace(query))
	for i, note := range notes {
		if i >= 5 {
			break
		}
		fmt.Fprintf(&b, "\n[%d] id=%s; type=%s; tags=%s\n%s\n", i+1, note.ID, note.ThoughtType, strings.Join(note.Tags, ", "), strings.TrimSpace(note.Content))
	}
	b.WriteString(`

Determine whether the notes establish a brain-specific meaning for the query. Return exactly this JSON shape:
{"interpretation":"...","expanded_terms":["..."],"confidence":0.0,"use_expansion":false}
Use at most three short expanded terms. Set use_expansion false when the notes do not clearly establish a specific meaning. Never expand based only on ordinary dictionary or world knowledge.`)
	return b.String()
}
