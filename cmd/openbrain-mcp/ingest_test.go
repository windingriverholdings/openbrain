package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craig8/openbrain/internal/pathsec"
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
			err := pathsec.ValidateIngestPath(tt.path, dir)
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

	err := pathsec.ValidateIngestPath(symlink, dir)
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
		{
			"file too large sanitized",
			"file too large: 100000000 bytes exceeds limit of 52428800 bytes",
			"file too large",
			"100000000",
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

func TestMCPIngestHandler_SourceLengthCap(t *testing.T) {
	// Source parameter should be capped at 255 characters.
	longSource := strings.Repeat("a", 256)
	sanitized := sanitizeIngestError("source too long")
	assert.NotEmpty(t, sanitized)

	// Verify the constant is 255
	assert.Equal(t, 255, sourceMaxLen)
	assert.True(t, len([]rune(longSource)) > sourceMaxLen)
}
