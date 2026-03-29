package brain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/craig8/openbrain/internal/config"
	"github.com/craig8/openbrain/internal/docparse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIngestDocument_MetadataKeys verifies that ingested metadata contains all
// expected standard keys using the docparse metadata constants.
func TestIngestDocument_MetadataKeys(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{IngestDir: dir}
	b := New(nil, nil, cfg)

	dest := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(dest, []byte("Some notes about architecture."), 0644))

	// We test the buildIngestMetadata helper directly.
	meta := buildIngestMetadata(dest, dir, "text", time.Now().UTC())

	assert.Equal(t, "notes.txt", meta[docparse.MetaSourceFile])
	assert.Equal(t, "notes.txt", meta[docparse.MetaSourcePath])
	assert.Equal(t, "text", meta[docparse.MetaSourceFormat])

	_, hasIngestedAt := meta[docparse.MetaIngestedAt]
	assert.True(t, hasIngestedAt, "metadata should contain %s", docparse.MetaIngestedAt)

	// Verify no absolute paths leaked into source_path
	sourcePath, ok := meta[docparse.MetaSourcePath].(string)
	require.True(t, ok)
	assert.False(t, strings.HasPrefix(sourcePath, "/"), "source_path must be relative, got: %s", sourcePath)

	_ = b // used for setup
}

// TestIngestMetadata_SourcePathRelative verifies source_path is relative to
// IngestDir for files in subdirectories.
func TestIngestMetadata_SourcePathRelative(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub", "folder")
	require.NoError(t, os.MkdirAll(subDir, 0755))

	filePath := filepath.Join(subDir, "deep.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("deep content"), 0644))

	meta := buildIngestMetadata(filePath, dir, "text", time.Now().UTC())

	sourcePath := meta[docparse.MetaSourcePath].(string)
	assert.Equal(t, filepath.Join("sub", "folder", "deep.txt"), sourcePath)
	assert.False(t, strings.HasPrefix(sourcePath, "/"), "source_path must never be absolute")
}

// TestIngestMetadata_IngestedAtRFC3339 verifies ingested_at is valid RFC3339.
func TestIngestMetadata_IngestedAtRFC3339(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.pdf")
	require.NoError(t, os.WriteFile(filePath, []byte("fake"), 0644))

	now := time.Now().UTC()
	meta := buildIngestMetadata(filePath, dir, "pdf", now)

	ingestedAt, ok := meta[docparse.MetaIngestedAt].(string)
	require.True(t, ok, "ingested_at should be a string")

	parsed, err := time.Parse(time.RFC3339, ingestedAt)
	require.NoError(t, err, "ingested_at should be valid RFC3339: %s", ingestedAt)
	assert.WithinDuration(t, now, parsed, time.Second)
}

// TestIngestChunkMetadata_UsesConstants verifies that chunk metadata uses the
// standard constants rather than string literals.
func TestIngestChunkMetadata_UsesConstants(t *testing.T) {
	meta := buildChunkMetadata("report.pdf", 2, 10)

	assert.Equal(t, "report.pdf", meta[docparse.MetaSourceFile])
	assert.Equal(t, 2, meta[docparse.MetaChunkIndex])
	assert.Equal(t, 10, meta[docparse.MetaChunkTotal])
}

// TestIngestDocument_FullMetadataFlow verifies that IngestDocument populates
// standard metadata when parsing a short document (no chunking).
func TestIngestDocument_FullMetadataFlow(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{IngestDir: dir}
	b := New(nil, nil, cfg)

	dest := filepath.Join(dir, "simple.txt")
	require.NoError(t, os.WriteFile(dest, []byte("Simple content."), 0644))

	// autoCapture=false just parses — verify the result message includes format
	result, err := b.IngestDocument(context.Background(), dest, "test", false)
	require.NoError(t, err)
	assert.Contains(t, result, "text")
	assert.Contains(t, result, "simple.txt")
}
