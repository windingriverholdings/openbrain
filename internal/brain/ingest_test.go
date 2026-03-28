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

	// Create a .txt file — unsupported
	dest := filepath.Join(dir, "notes.txt")
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

func TestValidateIngestPath_SecurityCases(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"empty path", "", "empty"},
		{"relative path", "docs/file.pdf", "absolute"},
		{"dot-dot traversal", filepath.Join(dir, "..", "etc", "passwd"), "outside allowed"},
		{"outside dir entirely", "/usr/share/doc/test.pdf", "outside allowed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIngestPath(tt.path, dir)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidateIngestPath_AcceptsValidPath(t *testing.T) {
	dir := t.TempDir()

	// Create a real file inside the dir
	validFile := filepath.Join(dir, "report.pdf")
	require.NoError(t, os.WriteFile(validFile, []byte("fake pdf"), 0644))

	err := validateIngestPath(validFile, dir)
	assert.NoError(t, err)
}
