package docparse

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dslipak/pdf"
)

type pdfParser struct{}

func (p *pdfParser) Parse(_ context.Context, filePath string) (ParseResult, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return ParseResult{}, fmt.Errorf("stat pdf: %w", err)
	}

	r, err := pdf.Open(filePath)
	if err != nil {
		return ParseResult{}, fmt.Errorf("open pdf: %w", err)
	}

	pageCount := r.NumPage()
	var pages []string
	for i := 1; i <= pageCount; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		trimmed := strings.TrimSpace(text)
		if trimmed != "" {
			pages = append(pages, trimmed)
		}
	}

	fullText := strings.Join(pages, "\n\n")
	if fullText == "" {
		return ParseResult{}, fmt.Errorf("no extractable text in PDF (may be scanned — try OCR instead)")
	}

	return ParseResult{
		Text: fullText,
		Metadata: map[string]any{
			"filename":        filepath.Base(filePath),
			"format":          string(FormatPDF),
			MetaSourceFormat:  string(FormatPDF),
			"page_count":      pageCount,
			"file_size_bytes": info.Size(),
		},
	}, nil
}
