package brain

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/windingriverholdings/openbrain/internal/intent"
)

// TestDispatch_AmbiguousReturnsExplicitPromptWithoutSearching asserts that a
// non-interactive Dispatch call (CLI, Telegram, Slack all route through this
// same call) never silently searches or captures an ambiguous message: it
// returns the exact AmbiguousPrompt text, and the embedder is never invoked,
// proving no read-only search runs behind the scenes. OB-078.
func TestDispatch_AmbiguousReturnsExplicitPromptWithoutSearching(t *testing.T) {
	embedCalled := false
	b := &Brain{embedder: trackingEmbedder{&embedCalled}}

	parsed := intent.ParsedIntent{Intent: intent.Ambiguous, Text: "tent notes", ThoughtType: "note"}
	result, err := b.Dispatch(context.Background(), parsed, "cli")

	require.NoError(t, err)
	assert.Equal(t, AmbiguousPrompt, result)
	assert.False(t, embedCalled, "Dispatch must not search (or capture) an ambiguous message; it must ask instead")
}

// TestDispatch_AmbiguousPromptMentionsBothExplicitPrefixes asserts the
// disambiguation text itself tells the user how to disambiguate, using the
// same "find:" / "note:" prefixes intent.Parse already recognizes as search
// and capture triggers, so following the instruction actually works.
func TestDispatch_AmbiguousPromptMentionsBothExplicitPrefixes(t *testing.T) {
	assert.Contains(t, AmbiguousPrompt, "find:")
	assert.Contains(t, AmbiguousPrompt, "note:")

	// The prompt's own suggested prefixes must round-trip through intent.Parse
	// as the intents they claim to trigger, or the instruction is a lie.
	assert.Equal(t, intent.Search, intent.Parse("find: tent notes").Intent)
	assert.Equal(t, intent.Capture, intent.Parse("note: tent notes").Intent)
}
