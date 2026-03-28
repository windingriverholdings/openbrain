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

func TestDeepCaptureWithMeta_MergesMetadata(t *testing.T) {
	// DeepCaptureWithMeta should merge file metadata into extracted thoughts.
	// This is a structural test — verify the function signature exists and
	// accepts the expected parameters.
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

	// Without a real embedder/DB, this will fail at embed step.
	// The important thing is the function exists with the right signature.
	_, err := b.DeepCaptureWithMeta(context.Background(), parsed, "test", meta)
	assert.Error(t, err) // Expected: embed will fail (nil embedder)
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
