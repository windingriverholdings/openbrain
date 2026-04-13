// Package db — re-embed support for OB-024 (nomic-embed-text migration).
package db

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/craig8/openbrain/internal/embeddings"
)

// CircuitBreakerThreshold is the number of consecutive embedding failures
// before ReembedAll aborts early. This prevents hammering a dead Ollama
// instance for hours on systemic failure.
const CircuitBreakerThreshold = 5

// reembedBatchSize is the number of rows fetched per batch to avoid
// unbounded memory usage when many thoughts need re-embedding.
const reembedBatchSize = 100

// ReembedResult holds the outcome of a re-embed operation.
type ReembedResult struct {
	Total     int
	Succeeded int
	Failed    int
	Errors    []string
}

// reembedRow holds a single thought's ID and content for re-embedding.
type reembedRow struct {
	ID      string
	Content string
}

// ReembedDB abstracts the database operations needed by ReembedAll,
// enabling unit testing without a real PostgreSQL connection.
type ReembedDB interface {
	FetchNullEmbeddings(ctx context.Context, limit, offset int) ([]reembedRow, error)
	UpdateEmbedding(ctx context.Context, id string, vec []float32) error
}

// pgxReembedDB implements ReembedDB using a pgxpool.Pool.
type pgxReembedDB struct {
	pool *pgxpool.Pool
}

func (p *pgxReembedDB) FetchNullEmbeddings(ctx context.Context, limit, offset int) ([]reembedRow, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT id::text, content FROM thoughts WHERE embedding IS NULL ORDER BY created_at ASC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch null embeddings: %w", err)
	}
	defer rows.Close()

	var result []reembedRow
	for rows.Next() {
		var r reembedRow
		if err := rows.Scan(&r.ID, &r.Content); err != nil {
			return nil, fmt.Errorf("scan thought: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate thoughts: %w", err)
	}
	return result, nil
}

func (p *pgxReembedDB) UpdateEmbedding(ctx context.Context, id string, vec []float32) error {
	_, err := p.pool.Exec(ctx,
		`UPDATE thoughts SET embedding = $1::vector WHERE id = $2::uuid`,
		VecLiteral(vec), id,
	)
	return err
}

// NewReembedDB wraps a pgxpool.Pool as a ReembedDB.
func NewReembedDB(pool *pgxpool.Pool) ReembedDB {
	return &pgxReembedDB{pool: pool}
}

// ReembedAll fetches all thoughts with NULL embeddings in batches and
// re-embeds them using the provided embedder. It updates each row
// individually and reports progress via the optional progressFn callback.
//
// The progressFn receives (processed, total) where processed is the number
// of thoughts processed so far (both successes and failures) and total is
// the total number of thoughts to re-embed.
//
// Circuit breaker: after CircuitBreakerThreshold consecutive failures,
// ReembedAll aborts and returns an error explaining the systemic failure.
//
// Context cancellation is checked at the top of each iteration.
//
// Dimension validation: vectors returned by the embedder must match
// expectedDim; mismatches are treated as errors for that thought.
func ReembedAll(ctx context.Context, db ReembedDB, embedder embeddings.Embedder, expectedDim int, progressFn func(processed, total int)) (*ReembedResult, error) {
	if db == nil {
		return nil, fmt.Errorf("reembed: db is nil")
	}
	if embedder == nil {
		return nil, fmt.Errorf("reembed: embedder is nil")
	}

	// Fetch all thoughts in batches to avoid unbounded memory.
	// No separate COUNT query — use len(thoughts) after fetch (TOCTOU fix).
	var allThoughts []reembedRow
	for offset := 0; ; offset += reembedBatchSize {
		batch, err := db.FetchNullEmbeddings(ctx, reembedBatchSize, offset)
		if err != nil {
			return nil, fmt.Errorf("reembed: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		allThoughts = append(allThoughts, batch...)
	}

	if len(allThoughts) == 0 {
		return &ReembedResult{Total: 0}, nil
	}

	result := &ReembedResult{Total: len(allThoughts)}
	consecutiveFailures := 0

	for i, t := range allThoughts {
		// Check context cancellation at the top of each iteration.
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("reembed: %w", err)
		}

		vec, err := embedder.Embed(ctx, t.Content)
		if err != nil {
			result.Failed++
			errMsg := fmt.Sprintf("thought %s: %v", t.ID, err)
			result.Errors = append(result.Errors, errMsg)
			slog.Warn("reembed failed", "thought_id", t.ID, "error", err)
			consecutiveFailures++

			if consecutiveFailures >= CircuitBreakerThreshold {
				return result, fmt.Errorf("reembed: circuit breaker tripped after %d consecutive failures — is Ollama running?", consecutiveFailures)
			}
			if progressFn != nil {
				progressFn(i+1, result.Total)
			}
			continue
		}

		// Validate embedding dimension matches expected config.
		if len(vec) != expectedDim {
			result.Failed++
			errMsg := fmt.Sprintf("thought %s: dimension mismatch: got %d, expected %d", t.ID, len(vec), expectedDim)
			result.Errors = append(result.Errors, errMsg)
			slog.Warn("reembed dimension mismatch", "thought_id", t.ID, "got", len(vec), "expected", expectedDim)
			consecutiveFailures++

			if consecutiveFailures >= CircuitBreakerThreshold {
				return result, fmt.Errorf("reembed: circuit breaker tripped after %d consecutive failures — is Ollama running?", consecutiveFailures)
			}
			if progressFn != nil {
				progressFn(i+1, result.Total)
			}
			continue
		}

		err = db.UpdateEmbedding(ctx, t.ID, vec)
		if err != nil {
			result.Failed++
			errMsg := fmt.Sprintf("thought %s: update failed: %v", t.ID, err)
			result.Errors = append(result.Errors, errMsg)
			slog.Warn("reembed update failed", "thought_id", t.ID, "error", err)
			consecutiveFailures++

			if consecutiveFailures >= CircuitBreakerThreshold {
				return result, fmt.Errorf("reembed: circuit breaker tripped after %d consecutive failures — is Ollama running?", consecutiveFailures)
			}
			if progressFn != nil {
				progressFn(i+1, result.Total)
			}
			continue
		}

		// Success — reset circuit breaker counter.
		consecutiveFailures = 0
		result.Succeeded++
		if progressFn != nil {
			progressFn(i+1, result.Total)
		}
	}

	return result, nil
}

// CheckReembedResult inspects a ReembedResult and returns an error if any
// thoughts failed to re-embed. This is used by the CLI to set a non-zero
// exit code on failure.
func CheckReembedResult(result *ReembedResult) error {
	if result.Failed == 0 {
		return nil
	}
	if result.Succeeded == 0 {
		return fmt.Errorf("reembed: all %d thoughts failed to re-embed", result.Failed)
	}
	return fmt.Errorf("reembed: %d of %d thoughts failed to re-embed", result.Failed, result.Total)
}

// checkReembedResult is an alias for tests in this package.
var checkReembedResult = CheckReembedResult
