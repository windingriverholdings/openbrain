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
)

// markitdownParser extracts text from PPTX and XLSX files using the markitdown
// CLI tool. It shells out to the binary with explicit args (no shell
// interpolation) and captures stdout.
type markitdownParser struct {
	binPath string
}

// Parse runs markitdown against the given file and returns the extracted text.
func (p *markitdownParser) Parse(ctx context.Context, filePath string) (ParseResult, error) {
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
		return ParseResult{}, fmt.Errorf("markitdown exited with error: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
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
