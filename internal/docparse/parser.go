// Package docparse provides document text extraction for PDF, DOCX, and
// image files (via OCR). Each format implements the Parser interface.
package docparse

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/craig8/openbrain/internal/config"
)

// Format identifies a supported document type.
type Format string

const (
	FormatPDF  Format = "pdf"
	FormatOCR  Format = "ocr"
	FormatDOCX Format = "docx"
	FormatText Format = "text"
)

// textExtensions maps file extensions to FormatText.
var textExtensions = map[string]bool{
	".md": true, ".txt": true, ".csv": true, ".json": true,
	".yaml": true, ".yml": true, ".toml": true, ".xml": true, ".html": true,
	".go": true, ".py": true, ".js": true, ".ts": true,
	".sh": true, ".sql": true, ".log": true, ".cfg": true, ".ini": true,
	".conf": true, ".rst": true, ".tex": true,
}

// ParseResult holds extracted text and source document metadata.
type ParseResult struct {
	Text     string
	Metadata map[string]any
}

// Parser extracts text from a document file.
type Parser interface {
	Parse(ctx context.Context, filePath string) (ParseResult, error)
}

// DetectFormat determines the document format from the file extension.
func DetectFormat(filePath string) (Format, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	// Handle compound extensions like ".env.example"
	base := strings.ToLower(filepath.Base(filePath))
	if strings.HasSuffix(base, ".env.example") {
		return FormatText, nil
	}

	switch ext {
	case ".pdf":
		return FormatPDF, nil
	case ".png", ".jpg", ".jpeg", ".tiff", ".tif", ".bmp":
		return FormatOCR, nil
	case ".docx":
		return FormatDOCX, nil
	default:
		if textExtensions[ext] {
			return FormatText, nil
		}
		return "", fmt.Errorf("unsupported file format: %q", ext)
	}
}

// NewParser returns the appropriate Parser for the given format.
func NewParser(format Format, cfg *config.Config) (Parser, error) {
	switch format {
	case FormatPDF:
		return &pdfParser{}, nil
	case FormatOCR:
		langs := "eng"
		if cfg != nil && cfg.TesseractLangs != "" {
			langs = cfg.TesseractLangs
		}
		return &ocrParser{langs: langs}, nil
	case FormatDOCX:
		return &docxParser{}, nil
	case FormatText:
		maxBytes := int64(50 * 1024 * 1024) // 50 MB default
		if cfg != nil && cfg.IngestMaxBytes > 0 {
			maxBytes = cfg.IngestMaxBytes
		}
		return &textParser{maxBytes: maxBytes}, nil
	default:
		return nil, fmt.Errorf("no parser for format: %q", format)
	}
}
