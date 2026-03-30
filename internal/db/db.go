// Package db manages the PostgreSQL connection pool and provides data access
// functions for thoughts, search, temporal tracking, and statistics.
package db

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/craig8/openbrain/internal/model"
)

// NewPool creates a connection pool from a database URL.
func NewPool(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("parse db config: %w", err)
	}
	poolCfg.MinConns = 1
	poolCfg.MaxConns = 5

	p, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create db pool: %w", err)
	}

	slog.Info("db connected", "dsn", redactDSN(dbURL))
	return p, nil
}

// VecLiteral formats a float32 slice as a pgvector literal: [0.1,0.2,...].
func VecLiteral(embedding []float32) string {
	parts := make([]string, len(embedding))
	for i, v := range embedding {
		parts[i] = fmt.Sprintf("%g", v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// InsertThought creates a new thought and returns its UUID.
func InsertThought(ctx context.Context, p *pgxpool.Pool, content string, embedding []float32, thoughtType string, tags []string, source string, summary *string, metadata map[string]any) (string, error) {
	if len(embedding) == 0 {
		return "", fmt.Errorf("insert thought: empty embedding vector (must have at least 1 dimension)")
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	if tags == nil {
		tags = []string{}
	}

	var id string
	err := p.QueryRow(ctx, `
		INSERT INTO thoughts (content, summary, embedding, thought_type, tags, source, metadata)
		VALUES ($1, $2, $3::vector, $4::thought_type, $5, $6, $7)
		RETURNING id::text`,
		content, summary, VecLiteral(embedding), thoughtType, tags, source, metadata,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert thought: %w", err)
	}

	slog.Info("thought inserted", "id", id[:8], "type", thoughtType, "source", source)
	return id, nil
}

// scanThought scans a single thought row from a pgx.Rows iterator.
func scanThought(rows pgx.Rows) (model.ThoughtRow, error) {
	var t model.ThoughtRow
	err := rows.Scan(
		&t.ID, &t.Content, &t.Summary, &t.ThoughtType,
		&t.Tags, &t.Source, &t.CreatedAt, &t.Score,
	)
	return t, err
}

func redactDSN(dsn string) string {
	// Show host/db only, not credentials
	if idx := strings.Index(dsn, "@"); idx != -1 {
		return "***@" + dsn[idx+1:]
	}
	return "***"
}
