package main

import (
	"os"
	"path/filepath"
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
