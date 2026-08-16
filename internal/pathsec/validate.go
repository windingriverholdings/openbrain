// Package pathsec provides shared path validation for ingestion security.
// Both the brain layer and the MCP layer delegate to these functions.
package pathsec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateIngestPath validates that a file path is safe for ingestion:
//   - Must not be empty
//   - Must be absolute
//   - Must resolve (after cleaning and symlink eval) to within allowedDir
func ValidateIngestPath(path, allowedDir string) error {
	if path == "" {
		return fmt.Errorf("file path is empty")
	}

	if !filepath.IsAbs(path) {
		return fmt.Errorf("file path must be absolute, got relative path")
	}

	if allowedDir == "" {
		return fmt.Errorf("ingestion not configured: OPENBRAIN_INGEST_DIR not set")
	}

	// Clean the path to resolve any .. components
	cleaned := filepath.Clean(path)

	// Reject paths that still contain .. after cleaning (defense in depth)
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == ".." {
			return fmt.Errorf("path outside allowed ingestion directory")
		}
	}

	// Resolve the allowed directory (in case it contains symlinks)
	allowedResolved, err := filepath.EvalSymlinks(filepath.Clean(allowedDir))
	if err != nil {
		return fmt.Errorf("cannot resolve allowed directory: %w", err)
	}

	// Resolve symlinks on the cleaned path, walking up ancestors if parts do not exist
	resolved, err := resolveSymlinks(cleaned)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}

	// Final check: resolved path must still be within allowed directory
	if !strings.HasPrefix(resolved, allowedResolved+string(filepath.Separator)) && resolved != allowedResolved {
		return fmt.Errorf("path outside allowed ingestion directory")
	}

	return nil
}

func resolveSymlinks(p string) (string, error) {
	var parts []string
	curr := p
	for {
		resolved, err := filepath.EvalSymlinks(curr)
		if err == nil {
			for i := len(parts) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, parts[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			return p, nil
		}
		parts = append(parts, filepath.Base(curr))
		curr = parent
	}
}
