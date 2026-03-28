// Package chunker splits long text into overlapping windows for document
// ingestion. It prefers natural break points (paragraph, line, sentence)
// over arbitrary mid-word splits.
package chunker

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

// ChunkText splits text into overlapping windows of at most windowSize runes.
// overlap controls how many runes from the end of one chunk appear at the
// start of the next. Returns nil for empty input.
func ChunkText(text string, windowSize int, overlap int) []Chunk {
	// TODO: implement
	return nil
}
