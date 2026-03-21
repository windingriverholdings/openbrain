package db

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/craig8/openbrain/internal/model"
)

// SupersedeThought marks an old thought as superseded by a new one.
func SupersedeThought(ctx context.Context, p *pgxpool.Pool, oldID, newID string) error {
	_, err := p.Exec(ctx, `SELECT supersede_thought($1::uuid, $2::uuid)`, oldID, newID)
	if err != nil {
		return fmt.Errorf("supersede thought: %w", err)
	}
	slog.Info("thought superseded", "old", oldID[:8], "new", newID[:8])
	return nil
}

// GetThoughtTimeline returns all thoughts (current and superseded) linked to a subject.
func GetThoughtTimeline(ctx context.Context, p *pgxpool.Pool, subjectName string, topK int) ([]model.ThoughtRow, error) {
	rows, err := p.Query(ctx, `
		SELECT t.id::text, t.content, t.summary, t.thought_type::text,
		       t.tags, t.source, t.created_at,
		       NULL::float8 AS score
		FROM thoughts t
		JOIN thought_subjects ts ON ts.thought_id = t.id
		WHERE LOWER(ts.subject_name) = LOWER($1)
		ORDER BY t.created_at DESC
		LIMIT $2`,
		subjectName, topK,
	)
	if err != nil {
		return nil, fmt.Errorf("thought timeline: %w", err)
	}
	defer rows.Close()

	var results []model.ThoughtRow
	for rows.Next() {
		t, err := scanThought(rows)
		if err != nil {
			return nil, fmt.Errorf("scan timeline result: %w", err)
		}
		results = append(results, t)
	}
	return results, rows.Err()
}

// LinkSubjects associates a thought with entity subjects (people, tools, concepts).
func LinkSubjects(ctx context.Context, p *pgxpool.Pool, thoughtID string, subjects []model.SubjectLink) error {
	if len(subjects) == 0 {
		return nil
	}

	for _, s := range subjects {
		_, err := p.Exec(ctx, `
			INSERT INTO thought_subjects (thought_id, subject_name, subject_type)
			VALUES ($1::uuid, $2, $3)
			ON CONFLICT DO NOTHING`,
			thoughtID, s.Name, s.Type,
		)
		if err != nil {
			return fmt.Errorf("link subject %q: %w", s.Name, err)
		}
	}

	slog.Info("subjects linked", "thought", thoughtID[:8], "count", len(subjects))
	return nil
}
