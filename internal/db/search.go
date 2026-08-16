package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/windingriverholdings/openbrain/internal/model"
)

// SearchThoughts performs cosine similarity search against thought embeddings.
func SearchThoughts(ctx context.Context, p *pgxpool.Pool, embedding []float32, topK int, thoughtType string, tags []string, scoreThreshold float64, createdFrom, createdTo *time.Time) ([]model.ThoughtRow, error) {
	if len(embedding) == 0 {
		return nil, fmt.Errorf("search: empty embedding vector")
	}

	query := `
		SELECT id::text, content, summary, thought_type::text,
		       tags, source, metadata, created_at,
		       1 - (embedding <=> $1::vector) AS score
		FROM thoughts
		WHERE is_current = TRUE
		  AND embedding IS NOT NULL`

	args := []any{VecLiteral(embedding)}
	argN := 2

	if thoughtType != "" {
		query += fmt.Sprintf(" AND thought_type = $%d::thought_type", argN)
		args = append(args, thoughtType)
		argN++
	}

	if len(tags) > 0 {
		query += fmt.Sprintf(" AND tags && $%d", argN)
		args = append(args, tags)
		argN++
	}

	if createdFrom != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", argN)
		args = append(args, *createdFrom)
		argN++
	}

	if createdTo != nil {
		query += fmt.Sprintf(" AND created_at <= $%d", argN)
		args = append(args, *createdTo)
		argN++
	}

	query += fmt.Sprintf(`
		ORDER BY embedding <=> $1::vector
		LIMIT $%d`, argN)
	args = append(args, topK)

	rows, err := p.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search thoughts: %w", err)
	}
	defer rows.Close()

	var results []model.ThoughtRow
	for rows.Next() {
		t, err := scanThought(rows)
		if err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		if t.Score != nil && *t.Score >= scoreThreshold {
			results = append(results, t)
		}
	}
	return results, rows.Err()
}

// buildHybridSearchQuery constructs the hybrid_search SQL with every argument
// fully typed so the 8-arg overload resolves unambiguously. The embedding cast
// is dimensioned to embeddingDim (OPENBRAIN_EMBEDDING_DIM, default 768) so
// pgvector validates dimensionality and overload resolution stays unambiguous.
// The outer SELECT applies optional date-range filters without modifying the
// stored SQL function.
func buildHybridSearchQuery(embeddingDim int) string {
	return fmt.Sprintf(`
		SELECT id::text, content, summary, thought_type::text,
		       tags, source, metadata, created_at, combined_score
		FROM hybrid_search(
		         $1::text,
		         $2::vector(%d),
		         $3::integer,
		         $4::double precision,
		         $5::double precision,
		         $6::double precision,
		         $7::boolean,
		         $8::text)
		WHERE ($9::timestamptz IS NULL OR created_at >= $9)
		  AND ($10::timestamptz IS NULL OR created_at <= $10)
		ORDER BY combined_score DESC LIMIT $11`, embeddingDim)
}

// HybridSearchThoughts performs combined keyword (BM25) + semantic (cosine) search.
// thoughtType filters results to a specific thought_type; pass "" to skip filtering.
// createdFrom/createdTo optionally bound results by created_at; pass nil for no limit.
// embeddingDim is the active model's dimension (OPENBRAIN_EMBEDDING_DIM); the
// embedding argument is cast to vector(embeddingDim) so pgvector validates the
// dimension and overload resolution stays unambiguous.
func HybridSearchThoughts(ctx context.Context, p *pgxpool.Pool, queryText string, embedding []float32, topK int, keywordWeight, semanticWeight, scoreThreshold float64, includeHistory bool, thoughtType string, createdFrom, createdTo *time.Time, embeddingDim int) ([]model.ThoughtRow, error) {
	return HybridSearchThoughtsExcluding(ctx, p, queryText, embedding, topK, keywordWeight, semanticWeight, scoreThreshold, includeHistory, thoughtType, createdFrom, createdTo, embeddingDim, nil)
}

// HybridSearchThoughtsExcluding performs hybrid search while excluding rows
// already read during a bounded conversational retrieval session.
func HybridSearchThoughtsExcluding(ctx context.Context, p *pgxpool.Pool, queryText string, embedding []float32, topK int, keywordWeight, semanticWeight, scoreThreshold float64, includeHistory bool, thoughtType string, createdFrom, createdTo *time.Time, embeddingDim int, excludeIDs []string) ([]model.ThoughtRow, error) {
	if len(embedding) == 0 {
		return nil, fmt.Errorf("search: empty embedding vector")
	}

	currentOnly := !includeHistory

	// Pass filter_type as NULL when empty, so SQL applies no type filter.
	var filterType *string
	if thoughtType != "" {
		filterType = &thoughtType
	}

	// Use a larger inner match_count when a date range is active so the outer
	// WHERE clause has more rows to filter without silently dropping recall.
	innerK := topK * 2
	if createdFrom != nil || createdTo != nil {
		innerK = topK * 4
	}
	resultLimit := topK
	if len(excludeIDs) > 0 {
		// The SQL function ranks before exclusion, so retrieve enough rows to
		// replace every already-read candidate.
		innerK += len(excludeIDs) * 2
		resultLimit += len(excludeIDs)
	}

	query := buildHybridSearchQuery(embeddingDim)
	if len(excludeIDs) > 0 {
		query = fmt.Sprintf(`SELECT * FROM (%s) ranked WHERE id <> ALL($12::text[]) ORDER BY combined_score DESC`, query)
	}

	args := []any{
		queryText, VecLiteral(embedding), innerK,
		keywordWeight, semanticWeight, scoreThreshold, currentOnly, filterType,
		createdFrom, createdTo, resultLimit,
	}
	if len(excludeIDs) > 0 {
		args = append(args, excludeIDs)
	}
	rows, err := p.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("hybrid search: %w", err)
	}
	defer rows.Close()

	// SQL already applies min_score — no double threshold filtering in Go.
	var results []model.ThoughtRow
	for rows.Next() {
		t, err := scanThought(rows)
		if err != nil {
			return nil, fmt.Errorf("scan hybrid result: %w", err)
		}
		results = append(results, t)
	}
	return results, rows.Err()
}

// KeywordSearchThoughts performs full-text keyword search using tsvector/tsquery.
// thoughtType filters results to a specific thought_type; pass "" to skip filtering.
// createdFrom/createdTo optionally bound results by created_at; pass nil for no limit.
func KeywordSearchThoughts(ctx context.Context, p *pgxpool.Pool, queryText string, topK int, includeHistory bool, thoughtType string, createdFrom, createdTo *time.Time) ([]model.ThoughtRow, error) {
	query := `
		SELECT id::text, content, summary, thought_type::text,
		       tags, source, metadata, created_at,
		       ts_rank(fts_vector, websearch_to_tsquery('english', $1)) AS score
		FROM thoughts
		WHERE fts_vector @@ websearch_to_tsquery('english', $1)`

	if !includeHistory {
		query += " AND is_current = TRUE"
	}

	args := []any{queryText}
	argN := 2

	if thoughtType != "" {
		query += fmt.Sprintf(" AND thought_type = $%d::thought_type", argN)
		args = append(args, thoughtType)
		argN++
	}

	if createdFrom != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", argN)
		args = append(args, *createdFrom)
		argN++
	}

	if createdTo != nil {
		query += fmt.Sprintf(" AND created_at <= $%d", argN)
		args = append(args, *createdTo)
		argN++
	}

	query += fmt.Sprintf(" ORDER BY score DESC LIMIT $%d", argN)
	args = append(args, topK)

	rows, err := p.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	defer rows.Close()

	var results []model.ThoughtRow
	for rows.Next() {
		t, err := scanThought(rows)
		if err != nil {
			return nil, fmt.Errorf("scan keyword result: %w", err)
		}
		results = append(results, t)
	}
	return results, rows.Err()
}
