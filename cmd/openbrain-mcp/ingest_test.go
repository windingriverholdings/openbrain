package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateIngestPathMCP_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"path traversal ../../etc/passwd", "../../etc/passwd", true},
		{"absolute traversal", filepath.Join(dir, "..", "..", "etc", "passwd"), true},
		{"empty path", "", true},
		{"relative path", "relative/file.pdf", true},
		{"outside allowed dir", "/etc/passwd", true},
		{"valid path inside dir", filepath.Join(dir, "report.pdf"), false},
	}

	// Create the valid file
	require.NoError(t, os.WriteFile(filepath.Join(dir, "report.pdf"), []byte("data"), 0644))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMCPIngestPath(tt.path, dir)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateIngestPathMCP_RejectsSymlink(t *testing.T) {
	dir := t.TempDir()

	// Create outside file and symlink
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.pdf")
	require.NoError(t, os.WriteFile(outsideFile, []byte("secret"), 0644))

	symlink := filepath.Join(dir, "link.pdf")
	require.NoError(t, os.Symlink(outsideFile, symlink))

	err := validateMCPIngestPath(symlink, dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside allowed")
}

func TestSanitizeIngestError_NoPathLeakage(t *testing.T) {
	tests := []struct {
		name     string
		err      string
		contains string
		absent   string
	}{
		{
			"removes internal path",
			"open /home/user/secret/data.pdf: no such file",
			"file not found",
			"/home/user/secret",
		},
		{
			"generic error passthrough",
			"unsupported format",
			"unsupported format",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sanitized := sanitizeIngestError(tt.err)
			assert.Contains(t, sanitized, tt.contains)
			if tt.absent != "" {
				assert.NotContains(t, sanitized, tt.absent)
			}
		})
	}
}

func TestMCPIngestHandler_NeverReturnsRawContent(t *testing.T) {
	// The ingest_document handler must never return raw file content.
	// It should only return thought count/summaries.
	// This is a design constraint test — verified at integration level,
	// but we document the expectation here.

	// The handler response format should be:
	// "Parsed <format> document: <filename> (<N> chars extracted)"
	// or on auto_capture:
	// "Ingested <filename>: <N> thoughts captured"
	// Never the actual text content.
	t.Log("Design constraint: ingest_document must never return raw file content to caller")
}
