//go:build ocr

package docparse

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/otiai10/gosseract/v2"
)

type ocrParser struct {
	langs string
}

func (p *ocrParser) Parse(_ context.Context, filePath string) (ParseResult, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return ParseResult{}, fmt.Errorf("stat image: %w", err)
	}

	client := gosseract.NewClient()
	defer client.Close()

	if p.langs != "" {
		if err := client.SetLanguage(p.langs); err != nil {
			return ParseResult{}, fmt.Errorf("set tesseract language %q: %w", p.langs, err)
		}
	}

	if err := client.SetImage(filePath); err != nil {
		return ParseResult{}, fmt.Errorf("set image: %w", err)
	}

	text, err := client.Text()
	if err != nil {
		return ParseResult{}, fmt.Errorf("ocr extract: %w", err)
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return ParseResult{}, fmt.Errorf("no text extracted from image via OCR")
	}

	return ParseResult{
		Text: text,
		Metadata: map[string]any{
			"filename":        filepath.Base(filePath),
			"format":          string(FormatOCR),
			"file_size_bytes": info.Size(),
			"ocr_languages":   p.langs,
		},
	}, nil
}
