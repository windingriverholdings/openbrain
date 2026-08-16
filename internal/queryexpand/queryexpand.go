// Package queryexpand performs local-model, note-grounded search expansion.
package queryexpand

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

// StreamProvider emits the grounded interpretation as Ollama generates it.
// The final line must be SEARCH_QUERY: followed by the semantic query to use.
type StreamProvider interface {
	Provider
	StreamExpand(ctx context.Context, query string, notes []model.ThoughtRow, onChunk func(string) error) (Result, error)
}

type ollamaProvider struct {
	provider wrsllm.Provider
	model    string
	baseURL  string
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
		baseURL:  cfg.OllamaBaseURL,
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

func (p *ollamaProvider) StreamExpand(ctx context.Context, query string, notes []model.ThoughtRow, onChunk func(string) error) (Result, error) {
	if strings.TrimSpace(query) == "" {
		return Result{}, fmt.Errorf("cannot expand an empty query")
	}
	if len(notes) == 0 {
		return Result{}, nil
	}
	prompt := FormatStreamingPrompt(query, notes)
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
		{Role: "system", Content: "You interpret a personal knowledge base only from supplied notes. Do not invent facts or rely on general world knowledge."},
		{Role: "user", Content: prompt},
	}

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(reqPayload); err != nil {
		return Result{}, err
	}
	baseURL := strings.TrimSuffix(p.baseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/chat", &body)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("local search refinement: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		return Result{}, fmt.Errorf("local search refinement: ollama returned status %d: %s", resp.StatusCode, responseBody)
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
			return Result{}, fmt.Errorf("local search refinement: decode stream: %w", err)
		}
		if event.Message.Content != "" {
			raw.WriteString(event.Message.Content)
			if err := onChunk(event.Message.Content); err != nil {
				return Result{}, err
			}
		}
		if event.Done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return Result{}, fmt.Errorf("local search refinement: read stream: %w", err)
	}
	return ParseStreamingResponse(raw.String())
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

// FormatStreamingPrompt asks for a user-readable, grounded interpretation and
// one machine-readable refined vector query. The model has no retrieval control.
func FormatStreamingPrompt(query string, notes []model.ThoughtRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The user asked: %s\n\nThe first semantic matches from their brain are:\n", strings.TrimSpace(query))
	for i, note := range notes {
		if i >= 3 {
			break
		}
		fmt.Fprintf(&b, "\n[%d] %s\n", i+1, strings.TrimSpace(note.Content))
	}
	b.WriteString("\nExplain in 1-3 plain-language sentences what the query means in this brain, based only on these notes. Then, on the final line, write exactly `SEARCH_QUERY: ` followed by one concise semantic search phrase. Do not mention search modes, tags, filters, or JSON.")
	return b.String()
}

// ParseStreamingResponse extracts the final refined semantic query while
// preserving a conservative fallback when the model does not follow the format.
func ParseStreamingResponse(raw string) (Result, error) {
	marker := "SEARCH_QUERY:"
	index := strings.LastIndex(strings.ToUpper(raw), marker)
	if index < 0 {
		return Result{}, fmt.Errorf("refinement response did not include SEARCH_QUERY")
	}
	interpretation := strings.TrimSpace(raw[:index])
	query := strings.TrimSpace(raw[index+len(marker):])
	query = strings.Trim(query, "` \t\r\n")
	if query == "" {
		return Result{}, fmt.Errorf("refinement response included an empty SEARCH_QUERY")
	}
	return Result{Interpretation: interpretation, ExpandedTerms: []string{query}, Confidence: 1, UseExpansion: true}, nil
}
