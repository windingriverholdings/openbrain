package docparse

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type textParser struct {
	maxBytes int64
}

func (p *textParser) Parse(_ context.Context, filePath string) (ParseResult, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return ParseResult{}, fmt.Errorf("stat text file: %w", err)
	}

	if p.maxBytes > 0 && info.Size() > p.maxBytes {
		return ParseResult{}, fmt.Errorf(
			"file size %d exceeds maximum %d bytes",
			info.Size(), p.maxBytes,
		)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return ParseResult{}, fmt.Errorf("read text file: %w", err)
	}

	text := string(data)
	lineCount := 0
	if len(data) > 0 {
		lineCount = bytes.Count(data, []byte("\n"))
		if !bytes.HasSuffix(data, []byte("\n")) {
			lineCount++
		}
	}

	return ParseResult{
		Text: text,
		Metadata: map[string]any{
			"filename":        filepath.Base(filePath),
			"format":          string(FormatText),
			"extension":       strings.ToLower(filepath.Ext(filePath)),
			"file_size_bytes": info.Size(),
			"line_count":      lineCount,
		},
	}, nil
}
