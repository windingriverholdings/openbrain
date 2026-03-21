// Package extract turns long-form text into structured thought candidates
// using an LLM provider.
package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/craig8/openbrain/internal/llm"
)

const extractionSystem = "You are a knowledge extraction assistant for a personal knowledge base. " +
	"Extract distinct, standalone thoughts from the input text. " +
	"Return ONLY a valid JSON array — no markdown fences, no commentary."

const extractionPrompt = `Analyze this text and extract distinct thoughts. For each thought, provide:
- content: the core information (1-3 sentences, standalone — someone reading it without context should understand it)
- thought_type: one of decision, insight, person, meeting, idea, note, memory
- tags: relevant tags as a list of lowercase strings
- subjects: people, tools, places, or concepts this thought is about (list of strings)
- supersedes_query: if this updates a previous fact, a search query to find the old thought (null otherwise)

Text to analyze:
%s

Return a JSON array of objects. Example format:
[
  {
    "content": "Decided to switch from Redis to Valkey for session caching.",
    "thought_type": "decision",
    "tags": ["caching", "infrastructure"],
    "subjects": ["Valkey", "Redis"],
    "supersedes_query": "Redis caching decision"
  }
]`

var validThoughtTypes = map[string]bool{
	"decision": true, "insight": true, "person": true,
	"meeting": true, "idea": true, "note": true, "memory": true,
}

// Candidate represents a structured thought extracted from text.
type Candidate struct {
	Content         string   `json:"content"`
	ThoughtType     string   `json:"thought_type"`
	Tags            []string `json:"tags"`
	Subjects        []string `json:"subjects"`
	SupersedesQuery *string  `json:"supersedes_query"`
}

// ParseExtractionResponse parses LLM output into thought candidates,
// handling markdown fences, embedded JSON, and validation.
func ParseExtractionResponse(raw string) []Candidate {
	text := strings.TrimSpace(raw)

	// Strip markdown code fences if present
	if strings.HasPrefix(text, "```") {
		var lines []string
		for _, ln := range strings.Split(text, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(ln), "```") {
				lines = append(lines, ln)
			}
		}
		text = strings.TrimSpace(strings.Join(lines, "\n"))
	}

	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		// Try to find a JSON array in the response
		start := strings.Index(text, "[")
		end := strings.LastIndex(text, "]")
		if start != -1 && end != -1 && end > start {
			if err2 := json.Unmarshal([]byte(text[start:end+1]), &parsed); err2 != nil {
				slog.Warn("extraction json parse failed", "raw_len", len(raw))
				return nil
			}
		} else {
			slog.Warn("extraction no json found", "raw_len", len(raw))
			return nil
		}
	}

	// Normalize to array
	items, ok := parsed.([]any)
	if !ok {
		items = []any{parsed}
	}

	var candidates []Candidate
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}

		content := strings.TrimSpace(stringFromMap(obj, "content"))
		if content == "" {
			continue
		}

		thoughtType := stringFromMap(obj, "thought_type")
		if !validThoughtTypes[thoughtType] {
			thoughtType = "note"
		}

		tags := stringListFromMap(obj, "tags")
		for i, t := range tags {
			tags[i] = strings.ToLower(strings.TrimSpace(t))
		}

		subjects := stringListFromMap(obj, "subjects")
		for i, s := range subjects {
			subjects[i] = strings.TrimSpace(s)
		}

		var supersedesQuery *string
		if sq, ok := obj["supersedes_query"].(string); ok && sq != "" {
			supersedesQuery = &sq
		}

		candidates = append(candidates, Candidate{
			Content:         content,
			ThoughtType:     thoughtType,
			Tags:            tags,
			Subjects:        subjects,
			SupersedesQuery: supersedesQuery,
		})
	}

	return candidates
}

// ExtractThoughts uses an LLM to extract structured thought candidates from long-form text.
func ExtractThoughts(ctx context.Context, text string) ([]Candidate, error) {
	provider, err := llm.GetProvider(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("get llm provider: %w", err)
	}
	if provider == nil {
		return nil, nil
	}

	prompt := fmt.Sprintf(extractionPrompt, text)

	slog.Info("extraction starting", "text_len", len(text))
	raw, err := provider.Generate(ctx, prompt, extractionSystem)
	if err != nil {
		return nil, fmt.Errorf("llm generate: %w", err)
	}
	slog.Info("extraction raw response", "response_len", len(raw))

	candidates := ParseExtractionResponse(raw)
	slog.Info("extraction complete", "candidates", len(candidates))

	return candidates, nil
}

func stringFromMap(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func stringListFromMap(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var result []string
	for _, item := range arr {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			result = append(result, s)
		}
	}
	return result
}
