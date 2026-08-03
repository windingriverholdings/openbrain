package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/windingriverholdings/openbrain/internal/brain"
	"github.com/windingriverholdings/openbrain/internal/model"
)

// ── wsResponse backward compatibility ───────────────────────────────────────

// TestWsResponse_NonSearchOmitsResults is the backward-compatibility guarantee:
// a reply with no structured rows must serialize exactly as it did before the
// results field existed, i.e. with no "results" key at all. Older clients (and
// the non-card bubble path) must not observe a shape change.
func TestWsResponse_NonSearchOmitsResults(t *testing.T) {
	data, err := json.Marshal(wsResponse{
		Content:     "**OpenBrain** — help text",
		Intent:      "help",
		ThoughtType: "note",
	})
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))

	_, present := raw["results"]
	assert.False(t, present, "non-search replies must omit the results key entirely")
	assert.Equal(t, "help", raw["intent"])
	assert.NotEmpty(t, raw["content"], "content must stay populated for non-search intents")
}

// TestWsResponse_SearchIncludesResults pins the structured payload's JSON
// contract, which the frontend's card renderer reads field-by-field.
func TestWsResponse_SearchIncludesResults(t *testing.T) {
	data, err := json.Marshal(wsResponse{
		Content: "Found 1 thought(s):\n\n1. [decision] (0.91) — 2026-08-03\n   use Redis\n\n",
		Intent:  "search",
		Results: []wsSearchResult{{
			ID:        "abc-123",
			Score:     0.91,
			Type:      "decision",
			Tags:      []string{"infra"},
			Summary:   "session caching",
			Content:   "use Redis for session caching",
			CreatedAt: "2026-08-03",
		}},
	})
	require.NoError(t, err)

	var raw struct {
		Content string `json:"content"`
		Intent  string `json:"intent"`
		Results []struct {
			ID        string   `json:"id"`
			Score     float64  `json:"score"`
			Type      string   `json:"type"`
			Tags      []string `json:"tags"`
			Summary   string   `json:"summary"`
			Content   string   `json:"content"`
			CreatedAt string   `json:"created_at"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(data, &raw))

	require.Len(t, raw.Results, 1)
	got := raw.Results[0]
	assert.Equal(t, "abc-123", got.ID)
	assert.InDelta(t, 0.91, got.Score, 0.0001)
	assert.Equal(t, "decision", got.Type)
	assert.Equal(t, []string{"infra"}, got.Tags)
	assert.Equal(t, "session caching", got.Summary)
	assert.Equal(t, "use Redis for session caching", got.Content)
	assert.Equal(t, "2026-08-03", got.CreatedAt)

	assert.NotEmpty(t, raw.Content,
		"content must remain populated for search so non-card clients still work")
}

// ── toWSSearchResults ───────────────────────────────────────────────────────

func TestToWSSearchResults_MapsAllFields(t *testing.T) {
	summary := "a summary"
	score := 0.42
	created := time.Date(2026, 8, 3, 14, 30, 0, 0, time.UTC)

	got := toWSSearchResults([]model.ThoughtRow{{
		ID:          "id-1",
		Content:     "full body text",
		Summary:     &summary,
		ThoughtType: "insight",
		Tags:        []string{"a", "b"},
		CreatedAt:   created,
		Score:       &score,
	}})

	require.Len(t, got, 1)
	assert.Equal(t, "id-1", got[0].ID)
	assert.Equal(t, "full body text", got[0].Content)
	assert.Equal(t, "a summary", got[0].Summary)
	assert.Equal(t, "insight", got[0].Type)
	assert.Equal(t, []string{"a", "b"}, got[0].Tags)
	assert.InDelta(t, 0.42, got[0].Score, 0.0001)
	assert.Equal(t, "2026-08-03", got[0].CreatedAt)
}

// TestToWSSearchResults_NilSummaryScoreAndTags covers the nil-pointer columns:
// a row with no summary, no score, and no tags must produce zero values and a
// non-nil (JSON array, not null) tags slice rather than panicking.
func TestToWSSearchResults_NilSummaryScoreAndTags(t *testing.T) {
	got := toWSSearchResults([]model.ThoughtRow{{
		ID:          "id-2",
		Content:     "body",
		ThoughtType: "note",
	}})

	require.Len(t, got, 1)
	assert.Empty(t, got[0].Summary)
	assert.Zero(t, got[0].Score)
	assert.NotNil(t, got[0].Tags, "tags must marshal as [] not null")
	assert.Empty(t, got[0].Tags)

	data, err := json.Marshal(got[0])
	require.NoError(t, err)
	assert.Contains(t, string(data), `"tags":[]`)
}

// TestToWSSearchResults_EmptyRowsMarshalsAsEmptyArray confirms a zero-hit search
// still yields a non-nil slice, so the frontend's Array.isArray(results) branch
// is taken (and it renders the "no matching notes" empty state) instead of
// falling through to the chat-bubble path.
func TestToWSSearchResults_EmptyRowsMarshalsAsEmptyArray(t *testing.T) {
	got := toWSSearchResults(nil)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// TestToWSSearchResults_DoesNotTruncateContent guards the difference from
// apiSearchNodes, which truncates at 200 chars for its preview. The chat cards
// clamp visually and expand in place, so they need the untruncated body.
func TestToWSSearchResults_DoesNotTruncateContent(t *testing.T) {
	long := make([]byte, 500)
	for i := range long {
		long[i] = 'x'
	}

	got := toWSSearchResults([]model.ThoughtRow{{
		ID:          "id-3",
		Content:     string(long),
		ThoughtType: "note",
	}})

	require.Len(t, got, 1)
	assert.Len(t, got[0].Content, 500, "websocket search results must carry the full body")
	assert.NotContains(t, got[0].Content, "…")
}

// ── shared formatter ────────────────────────────────────────────────────────

// TestFormatSearchResults_MatchesLegacyFormat pins the exact plain-text output
// the CLI and the MCP search tool render. Extracting this formatter out of
// formatSearch must be a pure refactor: if this string changes, those two
// surfaces changed too.
func TestFormatSearchResults_MatchesLegacyFormat(t *testing.T) {
	score := 0.87
	out := brain.FormatSearchResults([]model.ThoughtRow{{
		Content:     "use Redis for session caching",
		ThoughtType: "decision",
		CreatedAt:   time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC),
		Score:       &score,
	}})

	assert.Equal(t,
		"Found 1 thought(s):\n\n1. [decision] (0.87) — 2026-08-03\n   use Redis for session caching\n\n",
		out)
}

func TestFormatSearchResults_NoResults(t *testing.T) {
	assert.Equal(t, "No matching thoughts found.", brain.FormatSearchResults(nil))
}

// TestFormatSearchResults_OmitsScoreWhenNil confirms a row with no score
// renders without the parenthesised score, as before.
func TestFormatSearchResults_OmitsScoreWhenNil(t *testing.T) {
	out := brain.FormatSearchResults([]model.ThoughtRow{{
		Content:     "no score row",
		ThoughtType: "note",
		CreatedAt:   time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC),
	}})

	assert.Contains(t, out, "1. [note] — 2026-08-03")
	assert.NotContains(t, out, "(0.00)")
}

// ── frontend wiring ─────────────────────────────────────────────────────────

// TestChatPageRendersStructuredSearchResults asserts the chat page routes
// search replies to the structured card renderer instead of the chat bubble,
// and that the card affordances (detail open, expand toggle) are wired.
func TestChatPageRendersStructuredSearchResults(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("static", "index.html"))
	require.NoError(t, err)
	page := string(data)

	// Search intent must branch away from the bubble path.
	assert.Contains(t, page, "data.intent === 'search' && Array.isArray(data.results)")
	assert.Contains(t, page, "appendSearchResults(data)")

	// Structured list + card building.
	assert.Contains(t, page, "function appendSearchResults(")
	assert.Contains(t, page, "function buildResultCard(")
	assert.Contains(t, page, "results-empty")
	assert.Contains(t, page, "score-tooltip")
	assert.Contains(t, page, "semantic similarity (70%) and keyword matching (30%)")
	assert.Contains(t, page, "score.setAttribute('aria-label', 'Relevance score '")

	// Clamped preview with an expand toggle.
	assert.Contains(t, page, "result-body")
	assert.Contains(t, page, "Show more")
	assert.Contains(t, page, "aria-expanded")

	// Block-aware markdown for note bodies.
	assert.Contains(t, page, "function renderNoteBody(")

	// Clicking a card opens the full note detail.
	assert.Contains(t, page, "function openDetail(")
	assert.Contains(t, page, "'/api/thought/'")

	// Note content must never be injected as markup.
	assert.NotContains(t, page, ".innerHTML = t.content")
	assert.NotContains(t, page, ".innerHTML = row.content")
}
