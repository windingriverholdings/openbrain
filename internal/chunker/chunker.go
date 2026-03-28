// Package chunker splits long text into overlapping windows for document
// ingestion. It prefers natural break points (paragraph, line, sentence)
// over arbitrary mid-word splits.
package chunker

import (
	"strings"
	"unicode/utf8"
)

// DefaultWindowSize is the default maximum rune count per chunk.
const DefaultWindowSize = 2000

// DefaultOverlap is the default number of overlapping runes between
// consecutive chunks.
const DefaultOverlap = 200

// Chunk represents a single text window within a larger document.
type Chunk struct {
	Text        string
	Index       int
	Total       int
	StartOffset int
}

// breakCandidates lists natural break sequences in priority order.
// The chunker searches for the highest-priority break within the last 20%
// of the window before falling back to an exact rune-aligned cut.
var breakCandidates = []string{"\n\n", "\n", ". "}

// ChunkText splits text into overlapping windows of at most windowSize runes.
// overlap controls how many runes from the end of one chunk appear at the
// start of the next. Returns nil for empty input.
//
// The function is unicode-safe: it operates on runes, never splitting a
// multi-byte character. When possible, it breaks at natural boundaries
// (paragraph, line, sentence) found within the last 20% of each window.
func ChunkText(text string, windowSize int, overlap int) []Chunk {
	runes := []rune(text)
	totalRunes := len(runes)

	if totalRunes == 0 {
		return nil
	}

	if totalRunes <= windowSize {
		return []Chunk{{
			Text:        text,
			Index:       0,
			Total:       1,
			StartOffset: 0,
		}}
	}

	// First pass: collect (start, end) rune-index pairs for each chunk.
	type span struct{ start, end int }
	var spans []span

	pos := 0
	for pos < totalRunes {
		end := pos + windowSize
		if end >= totalRunes {
			// Last chunk: take everything remaining.
			spans = append(spans, span{pos, totalRunes})
			break
		}

		// Look for a natural break in the last 20% of the window.
		breakEnd := findNaturalBreak(runes, pos, end, windowSize)

		spans = append(spans, span{pos, breakEnd})

		// Advance: next chunk starts overlap runes before the end of this chunk.
		nextStart := breakEnd - overlap
		if nextStart <= pos {
			nextStart = pos + 1
		}
		pos = nextStart
	}

	// Second pass: build Chunk values with correct Total.
	chunks := make([]Chunk, len(spans))
	for i, s := range spans {
		chunks[i] = Chunk{
			Text:        string(runes[s.start:s.end]),
			Index:       i,
			Total:       len(spans),
			StartOffset: runeOffsetToByteOffset(text, s.start),
		}
	}

	return chunks
}

// findNaturalBreak searches for the best break point within the last 20% of
// the window [pos, end). Returns the rune index where the chunk should end.
func findNaturalBreak(runes []rune, pos, end, windowSize int) int {
	// Search zone: last 20% of the window.
	searchStart := end - windowSize/5
	if searchStart < pos {
		searchStart = pos
	}
	searchRegion := string(runes[searchStart:end])

	for _, sep := range breakCandidates {
		idx := strings.LastIndex(searchRegion, sep)
		if idx >= 0 {
			// Convert byte offset within searchRegion to a rune count,
			// including the separator so the break sits after it.
			runeIdx := utf8.RuneCountInString(searchRegion[:idx+len(sep)])
			return searchStart + runeIdx
		}
	}

	// No natural break found — cut at the exact window boundary.
	return end
}

// runeOffsetToByteOffset converts a rune offset to a byte offset within s.
func runeOffsetToByteOffset(s string, runeOffset int) int {
	byteOff := 0
	for i := 0; i < runeOffset && byteOff < len(s); i++ {
		_, size := utf8.DecodeRuneInString(s[byteOff:])
		byteOff += size
	}
	return byteOff
}
