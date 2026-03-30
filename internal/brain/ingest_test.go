package brain

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/craig8/openbrain/internal/config"
	"github.com/craig8/openbrain/internal/docparse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIngestDocument_DetectsFormat(t *testing.T) {
	// IngestDocument should detect format from file extension and parse.
	// Use a real PDF fixture from docparse testdata.
	dir := t.TempDir()
	cfg := &config.Config{IngestDir: dir}
	b := New(nil, nil, cfg)

	// Copy sample.pdf to temp dir
	src := filepath.Join("..", "docparse", "testdata", "sample.pdf")
	data, err := os.ReadFile(src)
	require.NoError(t, err)

	dest := filepath.Join(dir, "sample.pdf")
	require.NoError(t, os.WriteFile(dest, data, 0644))

	result, err := b.IngestDocument(context.Background(), dest, "test", false)
	require.NoError(t, err)
	assert.Contains(t, result, "Parsed")
	assert.Contains(t, result, "pdf")
}

func TestIngestDocument_RejectsUnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{IngestDir: dir}
	b := New(nil, nil, cfg)

	// Create a .doc file — unsupported
	dest := filepath.Join(dir, "notes.doc")
	require.NoError(t, os.WriteFile(dest, []byte("hello"), 0644))

	_, err := b.IngestDocument(context.Background(), dest, "test", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestIngestDocument_RejectsPathOutsideIngestDir(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{IngestDir: dir}
	b := New(nil, nil, cfg)

	_, err := b.IngestDocument(context.Background(), "/etc/passwd", "test", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside allowed")
}

func TestIngestDocument_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{IngestDir: dir}
	b := New(nil, nil, cfg)

	traversal := filepath.Join(dir, "..", "..", "etc", "passwd")
	_, err := b.IngestDocument(context.Background(), traversal, "test", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside allowed")
}

func TestIngestDocument_RejectsEmptyPath(t *testing.T) {
	cfg := &config.Config{IngestDir: "/tmp"}
	b := New(nil, nil, cfg)

	_, err := b.IngestDocument(context.Background(), "", "test", false)
	assert.Error(t, err)
}

func TestIngestDocument_RejectsRelativePath(t *testing.T) {
	cfg := &config.Config{IngestDir: "/tmp"}
	b := New(nil, nil, cfg)

	_, err := b.IngestDocument(context.Background(), "relative/path.pdf", "test", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "absolute")
}

func TestIngestDocument_RejectsSymlinkOutsideDir(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{IngestDir: dir}
	b := New(nil, nil, cfg)

	// Create a symlink inside dir that points outside
	outsideFile := filepath.Join(t.TempDir(), "outside.pdf")
	require.NoError(t, os.WriteFile(outsideFile, []byte("fake"), 0644))

	symlink := filepath.Join(dir, "sneaky.pdf")
	require.NoError(t, os.Symlink(outsideFile, symlink))

	_, err := b.IngestDocument(context.Background(), symlink, "test", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside allowed")
}

func TestIngestDocument_RejectsEmptyIngestDir(t *testing.T) {
	// When IngestDir is not configured, all ingestion should be rejected.
	cfg := &config.Config{IngestDir: ""}
	b := New(nil, nil, cfg)

	_, err := b.IngestDocument(context.Background(), "/tmp/test.pdf", "test", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestCheckFileSize_RejectsOversized(t *testing.T) {
	dir := t.TempDir()
	bigFile := filepath.Join(dir, "big.pdf")

	// Write a file that's 1024 bytes
	require.NoError(t, os.WriteFile(bigFile, make([]byte, 1024), 0644))

	// Limit of 512 bytes should reject it
	err := checkFileSize(bigFile, 512)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "file too large")

	// Limit of 2048 bytes should accept it
	err = checkFileSize(bigFile, 2048)
	assert.NoError(t, err)
}

func TestCheckFileSize_FallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	smallFile := filepath.Join(dir, "small.pdf")
	require.NoError(t, os.WriteFile(smallFile, []byte("data"), 0644))

	// Zero maxBytes should use default (50 MB), so small file passes
	err := checkFileSize(smallFile, 0)
	assert.NoError(t, err)
}

func TestIngestDocument_RejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{IngestDir: dir, IngestMaxBytes: 100}
	b := New(nil, nil, cfg)

	// Create a file that exceeds 100 bytes
	dest := filepath.Join(dir, "large.pdf")
	require.NoError(t, os.WriteFile(dest, make([]byte, 200), 0644))

	_, err := b.IngestDocument(context.Background(), dest, "test", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "file too large")
}

func TestDeepCaptureWithMeta_SignatureExists(t *testing.T) {
	// Verify DeepCaptureWithMeta exists with the correct signature and
	// handles the no-candidates case gracefully.
	cfg := &config.Config{}
	b := New(nil, nil, cfg)

	parsed := docparse.ParseResult{
		Text: "Test document content about architecture decisions.",
		Metadata: map[string]any{
			"filename": "test.pdf",
			"format":   "pdf",
		},
	}
	meta := map[string]any{"custom_key": "custom_value"}

	// Without LLM configured, ExtractThoughts returns nil, nil.
	// DeepCaptureWithMeta should handle this gracefully.
	result, err := b.DeepCaptureWithMeta(context.Background(), parsed, "test", meta)
	assert.NoError(t, err)
	assert.Contains(t, result, "0 thoughts captured")
}

func TestMergeMetadata_ImmutableMerge(t *testing.T) {
	base := map[string]any{"filename": "test.pdf", "format": "pdf"}
	overlay := map[string]any{"custom_key": "value", "source": "test"}

	merged := mergeMetadata(base, overlay)

	// Merged should contain all keys
	assert.Equal(t, "test.pdf", merged["filename"])
	assert.Equal(t, "pdf", merged["format"])
	assert.Equal(t, "value", merged["custom_key"])
	assert.Equal(t, "test", merged["source"])

	// Original maps should be unchanged (immutability)
	assert.Len(t, base, 2)
	assert.Len(t, overlay, 2)
}

func TestIngestDocument_LongTextChunked(t *testing.T) {
	// A text file longer than IngestChunkSize should produce multiple chunks
	// in the parse-only (autoCapture=false) summary.
	dir := t.TempDir()
	cfg := &config.Config{
		IngestDir:       dir,
		IngestChunkSize: 500,
	}
	b := New(nil, nil, cfg)

	// Create a text file with ~2500 chars.
	longText := ""
	for i := 0; i < 50; i++ {
		longText += "This is paragraph number. Some filler text here.\n\n"
	}
	dest := filepath.Join(dir, "long.txt")
	require.NoError(t, os.WriteFile(dest, []byte(longText), 0644))

	result, err := b.IngestDocument(context.Background(), dest, "test", false)
	require.NoError(t, err)
	assert.Contains(t, result, "chunks")
	assert.Contains(t, result, "Parsed")
}

func TestIngestDocument_ShortTextNotChunked(t *testing.T) {
	// A text file shorter than IngestChunkSize should NOT mention chunks.
	dir := t.TempDir()
	cfg := &config.Config{
		IngestDir:       dir,
		IngestChunkSize: 5000,
	}
	b := New(nil, nil, cfg)

	dest := filepath.Join(dir, "short.txt")
	require.NoError(t, os.WriteFile(dest, []byte("Short doc."), 0644))

	result, err := b.IngestDocument(context.Background(), dest, "test", false)
	require.NoError(t, err)
	assert.Contains(t, result, "Parsed")
	assert.NotContains(t, result, "chunks")
}

func TestIngestDocument_ChunkMetadataIncluded(t *testing.T) {
	// When autoCapture is true and text is long enough to chunk, each chunk's
	// metadata should include chunk_index, chunk_total, and source_file.
	// Without LLM configured, DeepCaptureWithMeta returns "0 thoughts captured"
	// for each chunk. We verify the summary mentions the chunk count.
	dir := t.TempDir()
	cfg := &config.Config{
		IngestDir:       dir,
		IngestChunkSize: 200,
	}
	b := New(nil, nil, cfg)

	longText := ""
	for i := 0; i < 30; i++ {
		longText += "Sentence with some content here.\n\n"
	}
	dest := filepath.Join(dir, "longdoc.txt")
	require.NoError(t, os.WriteFile(dest, []byte(longText), 0644))

	result, err := b.IngestDocument(context.Background(), dest, "test", true)
	require.NoError(t, err)
	// Summary should mention chunks (e.g. "5 chunks")
	assert.Contains(t, result, "chunks")
}

func TestTruncate_RuneSafe(t *testing.T) {
	// ASCII: truncate at 5 runes
	assert.Equal(t, "hello", truncate("hello world", 5))

	// Short string: returned as-is
	assert.Equal(t, "hi", truncate("hi", 10))

	// Multi-byte: truncate at 3 runes should not split a character
	assert.Equal(t, "日本語", truncate("日本語テスト", 3))

	// Emoji: each emoji is one rune
	assert.Equal(t, "🎉🎊", truncate("🎉🎊🎈", 2))
}
