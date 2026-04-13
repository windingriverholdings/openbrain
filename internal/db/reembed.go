// Package db — re-embed support for OB-024 (nomic-embed-text migration).
package db

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/craig8/openbrain/internal/embeddings"
)

// ReembedResult holds the outcome of a re-embed operation.
type ReembedResult struct {
	Total     int
	Succeeded int
	Failed    int
	Errors    []string
}

// ReembedAll fetches all thoughts with NULL embeddings and re-embeds them
// using the provided embedder. It updates each row individually and reports
// progress via the optional progressFn callback (called after each thought).
func ReembedAll(ctx context.Context, pool *pgxpool.Pool, embedder embeddings.Embedder, progressFn func(done, total int)) (*ReembedResult, error) {
	if pool == nil {
		return nil, fmt.Errorf("reembed: pool is nil")
	}
	if embedder == nil {
		return nil, fmt.Errorf("reembed: embedder is nil")
	}

	// Count thoughts needing re-embedding (NULL embedding).
	var total int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM thoughts WHERE embedding IS NULL`).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("reembed: count null embeddings: %w", err)
	}

	if total == 0 {
		return &ReembedResult{Total: 0}, nil
	}

	// Fetch IDs and content for all thoughts with NULL embeddings.
	rows, err := pool.Query(ctx, `SELECT id::text, content FROM thoughts WHERE embedding IS NULL ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("reembed: fetch thoughts: %w", err)
	}
	defer rows.Close()

	type thought struct {
		ID      string
		Content string
	}
	var thoughts []thought
	for rows.Next() {
		var t thought
		if err := rows.Scan(&t.ID, &t.Content); err != nil {
			return nil, fmt.Errorf("reembed: scan thought: %w", err)
		}
		thoughts = append(thoughts, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reembed: iterate thoughts: %w", err)
	}

	result := &ReembedResult{Total: len(thoughts)}

	for i, t := range thoughts {
		vec, err := embedder.Embed(ctx, t.Content)
		if err != nil {
			result.Failed++
			errMsg := fmt.Sprintf("thought %s: %v", t.ID[:8], err)
			result.Errors = append(result.Errors, errMsg)
			slog.Warn("reembed failed", "thought_id", t.ID[:8], "error", err)
			continue
		}

		_, err = pool.Exec(ctx,
			`UPDATE thoughts SET embedding = $1::vector WHERE id = $2::uuid`,
			VecLiteral(vec), t.ID,
		)
		if err != nil {
			result.Failed++
			errMsg := fmt.Sprintf("thought %s: update failed: %v", t.ID[:8], err)
			result.Errors = append(result.Errors, errMsg)
			slog.Warn("reembed update failed", "thought_id", t.ID[:8], "error", err)
			continue
		}

		result.Succeeded++
		if progressFn != nil {
			progressFn(i+1, result.Total)
		}
	}

	return result, nil
}
