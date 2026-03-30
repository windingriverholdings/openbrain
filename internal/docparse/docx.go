package docparse

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fumiama/go-docx"
)

type docxParser struct{}

func (p *docxParser) Parse(_ context.Context, filePath string) (ParseResult, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return ParseResult{}, fmt.Errorf("stat docx: %w", err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return ParseResult{}, fmt.Errorf("open docx: %w", err)
	}
	defer f.Close()

	doc, err := docx.Parse(f, info.Size())
	if err != nil {
		return ParseResult{}, fmt.Errorf("parse docx: %w", err)
	}

	var paragraphs []string
	for _, item := range doc.Document.Body.Items {
		para, ok := item.(*docx.Paragraph)
		if !ok {
			continue
		}
		text := extractParagraphText(para)
		if text != "" {
			paragraphs = append(paragraphs, text)
		}
	}

	fullText := strings.Join(paragraphs, "\n")
	if fullText == "" {
		return ParseResult{}, fmt.Errorf("no extractable text in DOCX")
	}

	return ParseResult{
		Text: fullText,
		Metadata: map[string]any{
			"filename":        filepath.Base(filePath),
			"format":          string(FormatDOCX),
			MetaSourceFormat:  string(FormatDOCX),
			"paragraph_count": len(paragraphs),
			"file_size_bytes": info.Size(),
		},
	}, nil
}

func extractParagraphText(para *docx.Paragraph) string {
	var parts []string
	for _, child := range para.Children {
		run, ok := child.(*docx.Run)
		if !ok {
			continue
		}
		for _, rc := range run.Children {
			if t, ok := rc.(*docx.Text); ok {
				parts = append(parts, t.Text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}
