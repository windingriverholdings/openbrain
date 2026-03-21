package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/craig8/openbrain/internal/model"
)

// GetStats returns aggregate statistics about the knowledge base.
func GetStats(ctx context.Context, p *pgxpool.Pool) (*model.Stats, error) {
	s := &model.Stats{ByType: map[string]int{}}

	err := p.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE created_at > now() - interval '7 days'),
			COUNT(*) FILTER (WHERE created_at > now() - interval '1 day'),
			MIN(created_at),
			MAX(created_at)
		FROM thoughts WHERE is_current = TRUE`,
	).Scan(&s.Total, &s.ThisWeek, &s.Today, &s.OldestAt, &s.NewestAt)
	if err != nil {
		return nil, fmt.Errorf("get stats totals: %w", err)
	}

	rows, err := p.Query(ctx, `
		SELECT thought_type::text, COUNT(*)
		FROM thoughts
		WHERE is_current = TRUE
		GROUP BY thought_type
		ORDER BY COUNT(*) DESC`)
	if err != nil {
		return nil, fmt.Errorf("get stats by type: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var typ string
		var count int
		if err := rows.Scan(&typ, &count); err != nil {
			return nil, fmt.Errorf("scan type stat: %w", err)
		}
		s.ByType[typ] = count
	}

	return s, rows.Err()
}

// GetThoughtsSince returns all current thoughts from the past N days.
func GetThoughtsSince(ctx context.Context, p *pgxpool.Pool, days int) ([]model.ThoughtRow, error) {
	rows, err := p.Query(ctx, `
		SELECT id::text, content, summary, thought_type::text,
		       tags, source, created_at,
		       NULL::float8 AS score
		FROM thoughts
		WHERE is_current = TRUE
		  AND created_at > now() - make_interval(days => $1)
		ORDER BY created_at DESC`,
		days,
	)
	if err != nil {
		return nil, fmt.Errorf("get thoughts since: %w", err)
	}
	defer rows.Close()

	var results []model.ThoughtRow
	for rows.Next() {
		t, err := scanThought(rows)
		if err != nil {
			return nil, fmt.Errorf("scan recent thought: %w", err)
		}
		results = append(results, t)
	}
	return results, rows.Err()
}
