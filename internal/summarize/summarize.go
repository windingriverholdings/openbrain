// Package summarize produces read-only summaries of retrieved thoughts.
package summarize

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	wrsllm "github.com/windingriverholdings/wrs-llm"

	"github.com/windingriverholdings/openbrain/internal/config"
	"github.com/windingriverholdings/openbrain/internal/model"
)

const systemPrompt = "You summarize retrieved personal knowledge-base notes. Use only the supplied results. Do not invent facts or fill gaps with assumptions. Mention conflicts and uncertainty. Answer the user's search question directly. Keep the response concise and structured."

const (
	// DefaultMaxResults caps how many retrieved rows reach the prompt. The cap
	// exists so a deep search cannot overflow the local model's context window.
	DefaultMaxResults = 15

	maxContentRunes = 12000
	maxSummaryRunes = 4000
)

// Summary contains the generated presentation text and the number of source rows.
type Summary struct {
	Summary     string
	ResultCount int
	ModelUsed   string
}

// Provider is the local model seam used by the summarizer.
type Provider interface {
	Summarize(ctx context.Context, query string, results []model.ThoughtRow) (string, error)
}

// StreamProvider is a provider that supports streaming chunk-by-chunk.
type StreamProvider interface {
	Provider
	StreamSummarize(ctx context.Context, query string, results []model.ThoughtRow, onChunk func(string) error) (string, error)
}

// ModelNamer is implemented by providers that can report the configured model.
type ModelNamer interface {
	ModelName() string
}

type ollamaProvider struct {
	provider   wrsllm.Provider
	model      string
	maxResults int
	baseURL    string
}

// New creates a summarizer using the configured local model fallback order.
func New(cfg *config.Config) (Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration is unavailable")
	}
	modelName := cfg.SearchSummaryModel
	if modelName == "" {
		modelName = cfg.SearchAssistedModel
	}
	if modelName == "" {
		modelName = cfg.ExtractModelFast
	}
	if modelName == "" {
		modelName = cfg.ExtractModel
	}
	if modelName == "" {
		return nil, fmt.Errorf("no local model configured for search summarization")
	}
	maxResults := cfg.SearchSummaryTopK
	if maxResults <= 0 {
		maxResults = DefaultMaxResults
	}
	return &ollamaProvider{
		provider:   wrsllm.NewOllamaProvider(cfg.OllamaBaseURL, modelName, 0),
		model:      modelName,
		maxResults: maxResults,
		baseURL:    cfg.OllamaBaseURL,
	}, nil
}

func (p *ollamaProvider) Summarize(ctx context.Context, query string, results []model.ThoughtRow) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("cannot summarize an empty query")
	}
	if len(results) == 0 {
		return "No matching information was found in the retrieved notes.", nil
	}
	prompt := FormatPromptN(query, results, p.maxResults)
	raw, err := p.provider.Generate(ctx, prompt, systemPrompt)
	if err != nil {
		return "", fmt.Errorf("local search summarization: %w", err)
	}
	result := strings.TrimSpace(raw)
	if result == "" {
		return "", fmt.Errorf("local search summarization returned an empty response")
	}
	return result, nil
}

func (p *ollamaProvider) ModelName() string { return p.model }

func (p *ollamaProvider) StreamSummarize(ctx context.Context, query string, results []model.ThoughtRow, onChunk func(string) error) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("cannot summarize an empty query")
	}
	if len(results) == 0 {
		msg := "No matching information was found in the retrieved notes."
		_ = onChunk(msg)
		return msg, nil
	}
	prompt := FormatPromptN(query, results, p.maxResults)

	reqPayload := struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
		System string `json:"system"`
		Stream bool   `json:"stream"`
	}{
		Model:  p.model,
		Prompt: prompt,
		System: systemPrompt,
		Stream: true,
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqPayload); err != nil {
		return "", err
	}

	apiURL := p.baseURL
	if apiURL == "" {
		apiURL = "http://localhost:11434"
	}
	apiURL = strings.TrimSuffix(apiURL, "/") + "/api/generate"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var fullResponse strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk struct {
			Response string `json:"response"`
			Done     bool   `json:"done"`
		}
		if err := json.Unmarshal(line, &chunk); err != nil {
			return "", err
		}
		if chunk.Response != "" {
			fullResponse.WriteString(chunk.Response)
			if err := onChunk(chunk.Response); err != nil {
				return "", err
			}
		}
		if chunk.Done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	return fullResponse.String(), nil
}

// FormatPrompt creates a bounded prompt using the default result cap.
func FormatPrompt(query string, results []model.ThoughtRow) string {
	return FormatPromptN(query, results, DefaultMaxResults)
}

// FormatPromptN creates a bounded prompt containing only the fields useful for
// summarization. Both the row count and the total content length are capped so
// a deep search cannot exceed the local model's context window.
func FormatPromptN(query string, results []model.ThoughtRow, maxResults int) string {
	if maxResults <= 0 {
		maxResults = DefaultMaxResults
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Search question:\n%s\n\nRetrieved notes:\n", strings.TrimSpace(query))
	used := 0
	for i, row := range results {
		if i >= maxResults || used >= maxContentRunes {
			break
		}
		content := strings.TrimSpace(row.Content)
		if content == "" {
			continue
		}
		remaining := maxContentRunes - used
		content = truncateRunes(content, remaining)
		used += len([]rune(content))
		fmt.Fprintf(&b, "\n[%d] id=%s; type=%s; source=%s; created=%s; tags=%s\n%s\n", i+1, row.ID, row.ThoughtType, row.Source, row.CreatedAt.Format(time.RFC3339), strings.Join(row.Tags, ", "), content)
		if row.Summary != nil && strings.TrimSpace(*row.Summary) != "" {
			fmt.Fprintf(&b, "Summary: %s\n", truncateRunes(strings.TrimSpace(*row.Summary), maxSummaryRunes))
		}
	}
	b.WriteString("\nSummarize only these retrieved notes. If they do not answer the question, say so.")
	return b.String()
}

// truncateRunes shortens s to at most max runes, including the ellipsis it
// appends, so a truncated value can never exceed the caller's budget.
func truncateRunes(s string, max int) string {
	const ellipsis = "..."
	runes := []rune(s)
	if max < 1 || len(runes) <= max {
		return s
	}
	keep := max - len(ellipsis)
	if keep < 1 {
		return string(runes[:max])
	}
	return string(runes[:keep]) + ellipsis
}
