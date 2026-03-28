// Package docparse provides document text extraction for PDF, DOCX, plain-text,
// and image files (via OCR). Each format implements the Parser interface.
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
	FormatPPTX Format = "pptx"
	FormatXLSX Format = "xlsx"
)

// textExtensions maps file extensions to FormatText.
var textExtensions = map[string]bool{
	".md": true, ".txt": true, ".csv": true, ".json": true,
	".yaml": true, ".yml": true, ".toml": true, ".xml": true, ".html": true,
	".go": true, ".py": true, ".js": true, ".ts": true,
	".sh": true, ".sql": true, ".log": true, ".cfg": true, ".ini": true,
	".conf": true, ".rst": true, ".tex": true,
}

// knownTextBasenames maps extensionless filenames and dotfiles that are known
// plain-text formats.
var knownTextBasenames = map[string]bool{
	"makefile":         true,
	"dockerfile":       true,
	"license":          true,
	".gitignore":       true,
	".gitattributes":   true,
	".dockerignore":    true,
	".editorconfig":    true,
	".env.example":     true,
	".env.local":       true,
	".env.development": true,
	".env.production":  true,
	".env.test":        true,
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
// Extensionless files and dotfiles are matched against knownTextBasenames.
func DetectFormat(filePath string) (Format, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	base := strings.ToLower(filepath.Base(filePath))

	// Check known extensionless/dotfile basenames first.
	if knownTextBasenames[base] {
		return FormatText, nil
	}

	switch ext {
	case ".pdf":
		return FormatPDF, nil
	case ".png", ".jpg", ".jpeg", ".tiff", ".tif", ".bmp":
		return FormatOCR, nil
	case ".docx":
		return FormatDOCX, nil
	case ".pptx":
		return FormatPPTX, nil
	case ".xlsx":
		return FormatXLSX, nil
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
		maxBytes := config.DefaultIngestMaxBytes
		if cfg != nil && cfg.IngestMaxBytes > 0 {
			maxBytes = cfg.IngestMaxBytes
		}
		return &textParser{maxBytes: maxBytes}, nil
	case FormatPPTX, FormatXLSX:
		binPath := "markitdown"
		if cfg != nil && cfg.MarkitdownPath != "" {
			binPath = cfg.MarkitdownPath
		}
		return &markitdownParser{binPath: binPath}, nil
	default:
		return nil, fmt.Errorf("no parser for format: %q", format)
	}
}
