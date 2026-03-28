package docparse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// maxStderrLen is the maximum number of characters from stderr included in
// error messages. Longer output is truncated to keep logs manageable.
const maxStderrLen = 256

// defaultSubprocessTimeout is applied when the caller's context has no
// deadline, preventing runaway child processes.
const defaultSubprocessTimeout = 60 * time.Second

// markitdownParser extracts text from PPTX and XLSX files using the markitdown
// CLI tool. It shells out to the binary with explicit args (no shell
// interpolation) and captures stdout.
type markitdownParser struct {
	binPath  string
	maxBytes int64
}

// truncateStderr returns s truncated to maxStderrLen characters.
func truncateStderr(s string) string {
	if len(s) <= maxStderrLen {
		return s
	}
	return s[:maxStderrLen] + "...(truncated)"
}

// Parse runs markitdown against the given file and returns the extracted text.
func (p *markitdownParser) Parse(ctx context.Context, filePath string) (ParseResult, error) {
	// If the parent context has no deadline, wrap with a default timeout to
	// prevent runaway subprocesses.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultSubprocessTimeout)
		defer cancel()
	}

	var stdout, stderr bytes.Buffer

	// Use exec.CommandContext for cancellation support.
	// Explicit args only — no shell interpolation.
	cmd := exec.CommandContext(ctx, p.binPath, filePath)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Distinguish "binary not found" from other errors.
		var pathErr *exec.Error
		if errors.As(err, &pathErr) || errors.Is(err, os.ErrNotExist) {
			return ParseResult{}, fmt.Errorf("markitdown binary not found at %q: %w", p.binPath, err)
		}
		stderrStr := truncateStderr(strings.TrimSpace(stderr.String()))
		return ParseResult{}, fmt.Errorf("markitdown exited with error: %w (stderr: %s)", err, stderrStr)
	}

	// Enforce stdout size limit if configured.
	if p.maxBytes > 0 && int64(stdout.Len()) > p.maxBytes {
		return ParseResult{}, fmt.Errorf(
			"markitdown output (%d bytes) exceeds maximum allowed size (%d bytes)",
			stdout.Len(), p.maxBytes,
		)
	}

	text := strings.TrimSpace(stdout.String())

	ext := strings.ToLower(filepath.Ext(filePath))
	format := string(FormatPPTX)
	if ext == ".xlsx" {
		format = string(FormatXLSX)
	}

	return ParseResult{
		Text: text,
		Metadata: map[string]any{
			"filename": filepath.Base(filePath),
			"format":   format,
		},
	}, nil
}
