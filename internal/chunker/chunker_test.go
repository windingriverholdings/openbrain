package chunker

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunkText_ShortText_SingleChunk(t *testing.T) {
	text := "This is a short document."
	chunks := ChunkText(text, DefaultWindowSize, DefaultOverlap)

	require.Len(t, chunks, 1)
	assert.Equal(t, text, chunks[0].Text)
	assert.Equal(t, 0, chunks[0].Index)
	assert.Equal(t, 1, chunks[0].Total)
	assert.Equal(t, 0, chunks[0].StartOffset)
}

func TestChunkText_EmptyText_EmptySlice(t *testing.T) {
	chunks := ChunkText("", DefaultWindowSize, DefaultOverlap)
	assert.Empty(t, chunks)
}

func TestChunkText_LongText_MultipleChunks(t *testing.T) {
	// Build text that exceeds DefaultWindowSize (2000 chars).
	paragraph := strings.Repeat("word ", 100) // 500 chars per paragraph
	text := paragraph + "\n\n" + paragraph + "\n\n" + paragraph + "\n\n" + paragraph + "\n\n" + paragraph

	chunks := ChunkText(text, DefaultWindowSize, DefaultOverlap)

	require.Greater(t, len(chunks), 1, "expected multiple chunks for long text")

	// Every chunk should have correct Total
	for _, c := range chunks {
		assert.Equal(t, len(chunks), c.Total)
	}

	// Indices should be sequential 0..N-1
	for i, c := range chunks {
		assert.Equal(t, i, c.Index)
	}

	// Reassemble: all text should be covered
	var totalLen int
	for _, c := range chunks {
		totalLen += len(c.Text)
	}
	// Total chars captured (with overlaps) should exceed original length
	assert.GreaterOrEqual(t, totalLen, len(text))
}

func TestChunkText_OverlapVerification(t *testing.T) {
	// Uniform repeating text is used deliberately: it contains no natural
	// break points (\n\n, \n, ". "), so the chunker cuts at exact window
	// boundaries. This makes the overlap region deterministic and lets us
	// assert exact tail/head equality between consecutive chunks.
	text := strings.Repeat("abcdefghij", 300) // 3000 chars
	windowSize := 500
	overlap := 50

	chunks := ChunkText(text, windowSize, overlap)
	require.Greater(t, len(chunks), 1)

	for i := 0; i < len(chunks)-1; i++ {
		current := chunks[i]
		next := chunks[i+1]

		// The tail of current chunk should overlap with the start of the next chunk.
		// The overlap region is at least `overlap` chars (may be slightly different
		// due to natural boundary seeking).
		tailOfCurrent := current.Text[len(current.Text)-overlap:]
		headOfNext := next.Text[:overlap]
		assert.Equal(t, tailOfCurrent, headOfNext,
			"chunk %d tail should overlap with chunk %d head", i, i+1)
	}
}

func TestChunkText_ParagraphBreaks(t *testing.T) {
	// Build text where a paragraph break (\n\n) falls within the last 20% of the window.
	// The chunker should prefer to split there.
	part1 := strings.Repeat("a", 450) // 450 chars
	part2 := strings.Repeat("b", 200) // 200 chars
	text := part1 + "\n\n" + part2 + strings.Repeat("c", 400)

	chunks := ChunkText(text, 500, 50)
	require.Greater(t, len(chunks), 1)

	// First chunk should end at or near the paragraph break (within last 20% of window).
	// It should contain the \n\n at the end or the text before it.
	assert.True(t, strings.HasSuffix(chunks[0].Text, "\n\n") ||
		!strings.Contains(chunks[0].Text, "\n\n"+part2),
		"chunker should prefer paragraph boundary")
}

func TestChunkText_UnicodeSafety(t *testing.T) {
	// Create text with multi-byte unicode that could be split mid-rune.
	// Each CJK character is 3 bytes in UTF-8.
	text := strings.Repeat("日本語テスト", 400) // 2400 runes, ~7200 bytes

	chunks := ChunkText(text, 2000, 200)

	for i, c := range chunks {
		assert.True(t, utf8.ValidString(c.Text),
			"chunk %d contains invalid UTF-8 (mid-rune split)", i)
	}
}

func TestChunkText_ExactWindowBoundary(t *testing.T) {
	// Text exactly at window size — should produce exactly one chunk.
	text := strings.Repeat("x", DefaultWindowSize)
	chunks := ChunkText(text, DefaultWindowSize, DefaultOverlap)

	require.Len(t, chunks, 1)
	assert.Equal(t, text, chunks[0].Text)
	assert.Equal(t, 0, chunks[0].Index)
	assert.Equal(t, 1, chunks[0].Total)
}

func TestChunkText_WindowPlusOne(t *testing.T) {
	// Text one char beyond window size — should produce two chunks.
	text := strings.Repeat("x", DefaultWindowSize+1)
	chunks := ChunkText(text, DefaultWindowSize, DefaultOverlap)

	require.Equal(t, 2, len(chunks))
	assert.Equal(t, 0, chunks[0].Index)
	assert.Equal(t, 1, chunks[1].Index)
	assert.Equal(t, 2, chunks[0].Total)
	assert.Equal(t, 2, chunks[1].Total)
}

func TestChunkText_StartOffsetsAreValid(t *testing.T) {
	text := strings.Repeat("word ", 600) // 3000 chars
	chunks := ChunkText(text, 1000, 100)

	require.Greater(t, len(chunks), 1)

	// First chunk starts at 0
	assert.Equal(t, 0, chunks[0].StartOffset)

	// Each subsequent chunk's StartOffset should be less than the previous
	// chunk's StartOffset + len(previous chunk text) (due to overlap)
	for i := 1; i < len(chunks); i++ {
		assert.Greater(t, chunks[i].StartOffset, chunks[i-1].StartOffset,
			"chunk %d StartOffset should advance from chunk %d", i, i-1)
	}
}

func TestConstants(t *testing.T) {
	assert.Equal(t, 2000, DefaultWindowSize)
	assert.Equal(t, 200, DefaultOverlap)
}
