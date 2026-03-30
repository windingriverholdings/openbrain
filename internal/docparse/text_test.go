package docparse

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/craig8/openbrain/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTextParser_Parse(t *testing.T) {
	fixturePath := filepath.Join("testdata", "sample.txt")

	cfg := &config.Config{IngestMaxBytes: config.DefaultIngestMaxBytes}
	p, err := NewParser(FormatText, cfg)
	require.NoError(t, err)

	result, err := p.Parse(context.Background(), fixturePath)
	require.NoError(t, err)

	assert.Contains(t, result.Text, "Hello, this is a sample text file.")
	assert.Contains(t, result.Text, "Line three here.")

	// Metadata checks
	assert.Equal(t, "sample.txt", result.Metadata["filename"])
	assert.Equal(t, "text", result.Metadata["format"])
	assert.Equal(t, ".txt", result.Metadata["extension"])
	assert.Greater(t, result.Metadata["file_size_bytes"].(int64), int64(0))
	assert.Equal(t, 3, result.Metadata["line_count"])
}

func TestTextParser_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	emptyFile := filepath.Join(dir, "empty.txt")
	err := os.WriteFile(emptyFile, []byte{}, 0644)
	require.NoError(t, err)

	cfg := &config.Config{IngestMaxBytes: config.DefaultIngestMaxBytes}
	p, err := NewParser(FormatText, cfg)
	require.NoError(t, err)

	result, err := p.Parse(context.Background(), emptyFile)
	require.NoError(t, err)

	assert.Equal(t, "", result.Text)
	assert.Equal(t, 0, result.Metadata["line_count"])
}

func TestTextParser_ExceedsMaxBytes(t *testing.T) {
	dir := t.TempDir()
	bigFile := filepath.Join(dir, "big.txt")
	err := os.WriteFile(bigFile, make([]byte, 200), 0644)
	require.NoError(t, err)

	cfg := &config.Config{IngestMaxBytes: 100}
	p, err := NewParser(FormatText, cfg)
	require.NoError(t, err)

	_, err = p.Parse(context.Background(), bigFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestTextParser_FileNotFound(t *testing.T) {
	cfg := &config.Config{IngestMaxBytes: config.DefaultIngestMaxBytes}
	p, err := NewParser(FormatText, cfg)
	require.NoError(t, err)

	_, err = p.Parse(context.Background(), "/nonexistent/file.txt")
	assert.Error(t, err)
}

func TestDetectFormat_TextExtensions(t *testing.T) {
	textExts := []string{
		".md", ".txt", ".csv", ".json", ".yaml", ".yml", ".toml", ".xml", ".html",
		".go", ".py", ".js", ".ts", ".sh", ".sql", ".log", ".cfg", ".ini",
		".conf", ".rst", ".tex",
	}

	for _, ext := range textExts {
		t.Run(ext, func(t *testing.T) {
			got, err := DetectFormat("testfile" + ext)
			require.NoError(t, err, "extension %s should be recognized", ext)
			assert.Equal(t, FormatText, got, "extension %s should map to FormatText", ext)
		})
	}
}

func TestDetectFormat_KnownBasenames(t *testing.T) {
	basenames := []string{
		"Makefile", "Dockerfile", "LICENSE",
		".gitignore", ".gitattributes", ".dockerignore", ".editorconfig",
		".env.example", ".env.local", ".env.development", ".env.production", ".env.test",
	}

	for _, name := range basenames {
		t.Run(name, func(t *testing.T) {
			got, err := DetectFormat(name)
			require.NoError(t, err, "basename %s should be recognized", name)
			assert.Equal(t, FormatText, got, "basename %s should map to FormatText", name)
		})
	}
}

func TestTextParser_NoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "no-newline.txt")
	err := os.WriteFile(f, []byte("line one\nline two"), 0644)
	require.NoError(t, err)

	cfg := &config.Config{IngestMaxBytes: config.DefaultIngestMaxBytes}
	p, err := NewParser(FormatText, cfg)
	require.NoError(t, err)

	result, err := p.Parse(context.Background(), f)
	require.NoError(t, err)

	assert.Equal(t, "line one\nline two", result.Text)
	assert.Equal(t, 2, result.Metadata["line_count"])
}
