package summarize

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/windingriverholdings/openbrain/internal/model"
)

func TestFormatPromptIncludesUsefulFieldsAndBoundsContent(t *testing.T) {
	created := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", maxContentRunes+100)
	summary := "A concise note summary"
	prompt := FormatPrompt("caching decisions", []model.ThoughtRow{{
		ID: "note-1", Content: long, Summary: &summary, ThoughtType: "decision",
		Tags: []string{"backend", "cache"}, Source: "web", CreatedAt: created,
	}})

	assert.Contains(t, prompt, "caching decisions")
	assert.Contains(t, prompt, "id=note-1")
	assert.Contains(t, prompt, "type=decision")
	assert.Contains(t, prompt, "source=web")
	assert.Contains(t, prompt, "backend, cache")
	assert.Contains(t, prompt, "A concise note summary")
	assert.LessOrEqual(t, strings.Count(prompt, "x"), maxContentRunes)
	assert.NotContains(t, prompt, "unsupported private metadata")
}

func TestFormatPromptCapsResultCount(t *testing.T) {
	rows := make([]model.ThoughtRow, DefaultMaxResults+2)
	for i := range rows {
		rows[i].ID = string(rune('a' + i))
		rows[i].Content = "note"
	}
	prompt := FormatPrompt("query", rows)
	assert.Contains(t, prompt, "id=o", "the last row within the default cap must be present")
	assert.NotContains(t, prompt, "id=p", "rows past the default cap must be dropped")
}

func TestFormatPromptHandlesEmptyResults(t *testing.T) {
	prompt := FormatPrompt("unknown", nil)
	assert.Contains(t, prompt, "Search question:\nunknown")
	assert.Contains(t, prompt, "Retrieved notes:")
}

// TestFormatPromptN_HonorsConfiguredCap asserts the row cap is configurable, so
// search depth and summary depth can be tuned independently.
func TestFormatPromptN_HonorsConfiguredCap(t *testing.T) {
	rows := make([]model.ThoughtRow, 30)
	for i := range rows {
		rows[i].ID = fmt.Sprintf("id-%02d", i)
		rows[i].Content = "note body"
	}

	prompt := FormatPromptN("query", rows, 5)

	assert.Contains(t, prompt, "id=id-04")
	assert.NotContains(t, prompt, "id=id-05", "rows past the configured cap must be dropped")
}

// TestFormatPromptN_InvalidCapFallsBackToDefault guards against a misconfigured
// cap silently producing an empty prompt.
func TestFormatPromptN_InvalidCapFallsBackToDefault(t *testing.T) {
	rows := make([]model.ThoughtRow, DefaultMaxResults+5)
	for i := range rows {
		rows[i].ID = fmt.Sprintf("id-%02d", i)
		rows[i].Content = "note body"
	}

	for _, cap := range []int{0, -3} {
		prompt := FormatPromptN("query", rows, cap)
		assert.Contains(t, prompt, "id=id-00")
		assert.Contains(t, prompt, fmt.Sprintf("id=id-%02d", DefaultMaxResults-1))
		assert.NotContains(t, prompt, fmt.Sprintf("id=id-%02d", DefaultMaxResults))
	}
}

// TestFormatPromptN_StaysWithinContentBudget asserts the total content budget is
// enforced even when the row cap would otherwise allow more, so a few very long
// notes cannot overflow the model's context window.
func TestFormatPromptN_StaysWithinContentBudget(t *testing.T) {
	// The filler rune must not occur anywhere in the prompt template, otherwise
	// counting it would measure boilerplate as if it were note content.
	const filler = "\u00e9"
	rows := make([]model.ThoughtRow, 10)
	for i := range rows {
		rows[i].ID = fmt.Sprintf("id-%02d", i)
		rows[i].Content = strings.Repeat(filler, 5000)
	}

	prompt := FormatPromptN("query", rows, 10)

	assert.LessOrEqual(t, strings.Count(prompt, filler), maxContentRunes,
		"total note content must never exceed the rune budget")
}
