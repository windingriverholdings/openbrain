//go:build !ocr

package docparse

import (
	"context"
	"fmt"
)

type ocrParser struct {
	langs string
}

func (p *ocrParser) Parse(_ context.Context, _ string) (ParseResult, error) {
	return ParseResult{}, fmt.Errorf("OCR support not available: rebuild with -tags ocr (requires libtesseract-dev and tesseract-ocr)")
}
