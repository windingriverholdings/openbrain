package watcher

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/craig8/openbrain/internal/docparse"
)

// tempSuffixes are file extensions that indicate incomplete/temporary files.
var tempSuffixes = []string{".tmp", "~", ".swp", ".part", ".crdownload"}

// isTempFile returns true if the filename has a temporary file suffix.
func isTempFile(name string) bool {
	for _, suffix := range tempSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// isSupportedFormat returns true if the file extension is handled by docparse.
func isSupportedFormat(name string) bool {
	_, err := docparse.DetectFormat(name)
	return err == nil
}

// ParseWatchDirs splits a comma-separated list of directory paths.
func ParseWatchDirs(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	dirs := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			dirs = append(dirs, trimmed)
		}
	}
	return dirs
}

// validateWatchDirs filters dirs to only those that exist, are directories,
// and fall within IngestDir (resolved via EvalSymlinks to prevent symlink escape).
// Returns an error if any directory resolves outside IngestDir.
func validateWatchDirs(dirs []string, ingestDir string) ([]string, error) {
	if ingestDir == "" {
		return nil, fmt.Errorf("OPENBRAIN_INGEST_DIR must be set when using watch directories")
	}

	resolvedIngest, err := filepath.EvalSymlinks(ingestDir)
	if err != nil {
		return nil, fmt.Errorf("resolve IngestDir %q: %w", ingestDir, err)
	}
	resolvedIngest = filepath.Clean(resolvedIngest)

	valid := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil {
			slog.Warn("watch dir does not exist", "dir", dir, "error", err)
			continue
		}
		if !info.IsDir() {
			slog.Warn("watch dir is not a directory", "dir", dir)
			continue
		}

		resolvedDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			slog.Warn("cannot resolve watch dir symlinks", "dir", dir, "error", err)
			continue
		}
		resolvedDir = filepath.Clean(resolvedDir)

		// Verify the resolved path falls within IngestDir
		if !strings.HasPrefix(resolvedDir, resolvedIngest+string(filepath.Separator)) && resolvedDir != resolvedIngest {
			return nil, fmt.Errorf("watch dir %q resolves to %q which is outside IngestDir %q — refusing to start", dir, resolvedDir, resolvedIngest)
		}

		valid = append(valid, dir)
	}
	return valid, nil
}
