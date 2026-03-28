// Package pathsec provides shared path validation for ingestion security.
// Both the brain layer and the MCP layer delegate to these functions.
package pathsec

import (
	"fmt"
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

	// Quick prefix check before attempting symlink resolution
	if !strings.HasPrefix(cleaned, allowedResolved+string(filepath.Separator)) && cleaned != allowedResolved {
		return fmt.Errorf("path outside allowed ingestion directory")
	}

	// Resolve symlinks to get the real path (catches symlink escapes)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		// If file doesn't exist, resolve the parent directory
		resolved, err = filepath.EvalSymlinks(filepath.Dir(cleaned))
		if err != nil {
			return fmt.Errorf("cannot resolve path: %w", err)
		}
		resolved = filepath.Join(resolved, filepath.Base(cleaned))
	}

	// Final check: resolved path must still be within allowed directory
	if !strings.HasPrefix(resolved, allowedResolved+string(filepath.Separator)) && resolved != allowedResolved {
		return fmt.Errorf("path outside allowed ingestion directory")
	}

	return nil
}
