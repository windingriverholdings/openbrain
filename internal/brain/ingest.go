package brain

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/craig8/openbrain/internal/db"
	"github.com/craig8/openbrain/internal/docparse"
	"github.com/craig8/openbrain/internal/extract"
	"github.com/craig8/openbrain/internal/model"
	"github.com/craig8/openbrain/internal/pathsec"
)

// IngestDocument detects format, parses the file, and optionally auto-captures
// extracted thoughts. Returns a summary string — never raw file content.
func (b *Brain) IngestDocument(ctx context.Context, filePath, source string, autoCapture bool) (string, error) {
	if b.cfg.IngestDir == "" {
		return "", fmt.Errorf("ingestion not configured: OPENBRAIN_INGEST_DIR not set")
	}

	if err := pathsec.ValidateIngestPath(filePath, b.cfg.IngestDir); err != nil {
		return "", err
	}

	if err := checkFileSize(filePath, b.cfg.IngestMaxBytes); err != nil {
		return "", err
	}

	format, err := docparse.DetectFormat(filePath)
	if err != nil {
		return "", fmt.Errorf("detect format: %w", err)
	}

	parser, err := docparse.NewParser(format, b.cfg)
	if err != nil {
		return "", fmt.Errorf("create parser: %w", err)
	}

	parsed, err := parser.Parse(ctx, filePath)
	if err != nil {
		return "", fmt.Errorf("parse document: %w", err)
	}

	if !autoCapture {
		return fmt.Sprintf("Parsed %s document: %s (%d chars extracted)",
			format, filepath.Base(filePath), len(parsed.Text)), nil
	}

	meta := map[string]any{"ingested_from": source}
	result, err := b.DeepCaptureWithMeta(ctx, parsed, source, meta)
	if err != nil {
		return "", fmt.Errorf("deep capture: %w", err)
	}

	return fmt.Sprintf("Ingested %s: %s", filepath.Base(filePath), result), nil
}

// DeepCaptureWithMeta extracts multiple thoughts from a parsed document via LLM,
// merging file metadata into each captured thought.
func (b *Brain) DeepCaptureWithMeta(ctx context.Context, parsed docparse.ParseResult, source string, meta map[string]any) (string, error) {
	candidates, err := extract.ExtractThoughts(ctx, parsed.Text)
	if err != nil {
		slog.Warn("extraction failed during ingest", "error", err)
		return "", fmt.Errorf("extract thoughts: %w", err)
	}

	if len(candidates) == 0 {
		return "0 thoughts captured (no extractable content)", nil
	}

	merged := mergeMetadata(parsed.Metadata, meta)

	captured, errs := captureExtracted(ctx, b, candidates, source, merged)

	return formatCaptureResult(captured, errs), nil
}

// captureExtracted is the shared core for DeepCapture and DeepCaptureWithMeta.
// It embeds and stores each candidate, linking subjects as needed.
func captureExtracted(ctx context.Context, b *Brain, candidates []extract.Candidate, source string, metadata map[string]any) ([]string, []string) {
	var captured []string
	var errs []string

	for _, c := range candidates {
		embedding, err := b.embedder.Embed(ctx, c.Content)
		if err != nil {
			errs = append(errs, fmt.Sprintf("embed %q: %v", truncate(c.Content, 30), err))
			continue
		}

		id, err := db.InsertThought(ctx, b.pool, c.Content, embedding, c.ThoughtType, c.Tags, source, nil, metadata)
		if err != nil {
			errs = append(errs, fmt.Sprintf("insert: %v", err))
			continue
		}

		var subjects []model.SubjectLink
		for _, s := range c.Subjects {
			subjects = append(subjects, model.SubjectLink{Name: s, Type: "concept"})
		}
		if len(subjects) > 0 {
			if err := db.LinkSubjects(ctx, b.pool, id, subjects); err != nil {
				slog.Warn("failed to link subjects", "error", err)
			}
		}

		captured = append(captured, fmt.Sprintf("[%s] %s", c.ThoughtType, id[:8]))
	}

	return captured, errs
}

// formatCaptureResult builds a human-readable summary of captured thoughts.
func formatCaptureResult(captured, errs []string) string {
	result := fmt.Sprintf("%d thoughts captured: %s", len(captured), strings.Join(captured, ", "))
	if len(errs) > 0 {
		result += fmt.Sprintf("\n%d errors: %s", len(errs), strings.Join(errs, "; "))
	}
	return result
}

// mergeMetadata creates a new metadata map from base and overlay, without mutating either.
func mergeMetadata(base, overlay map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range overlay {
		merged[k] = v
	}
	return merged
}

// defaultIngestMaxBytes is the fallback file size limit (50 MB) when config is zero.
const defaultIngestMaxBytes int64 = 50 * 1024 * 1024

// checkFileSize rejects files that exceed the configured maximum size.
// A zero maxBytes value falls back to defaultIngestMaxBytes.
func checkFileSize(filePath string, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = defaultIngestMaxBytes
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	if info.Size() > maxBytes {
		return fmt.Errorf("file too large: %d bytes exceeds limit of %d bytes", info.Size(), maxBytes)
	}
	return nil
}

// truncate returns the first n runes of s, or s if shorter.
// Operates on runes to avoid splitting multi-byte UTF-8 characters.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
