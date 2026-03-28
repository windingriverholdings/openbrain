package pathsec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		{"empty allowed dir", "/tmp/test.pdf", "not configured"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowedDir := dir
			if tt.name == "empty allowed dir" {
				allowedDir = ""
			}
			err := ValidateIngestPath(tt.path, allowedDir)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidateIngestPath_AcceptsValidPath(t *testing.T) {
	dir := t.TempDir()
	validFile := filepath.Join(dir, "report.pdf")
	require.NoError(t, os.WriteFile(validFile, []byte("fake pdf"), 0644))

	err := ValidateIngestPath(validFile, dir)
	assert.NoError(t, err)
}

func TestValidateIngestPath_RejectsSymlinkOutsideDir(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.pdf")
	require.NoError(t, os.WriteFile(outsideFile, []byte("secret"), 0644))

	symlink := filepath.Join(dir, "sneaky.pdf")
	require.NoError(t, os.Symlink(outsideFile, symlink))

	err := ValidateIngestPath(symlink, dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside allowed")
}
