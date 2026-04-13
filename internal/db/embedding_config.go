// Package db — embedding config safety for OB-024e.
//
// EmbeddingConfigDB tracks the active embedding model and dimension in the
// database. All long-running binaries validate at startup that the env config
// matches the DB, preventing silent dimension mismatches after model changes.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EmbeddingConfigDB abstracts access to the embedding_config singleton row,
// enabling unit testing without a real PostgreSQL connection.
type EmbeddingConfigDB interface {
	GetEmbeddingConfig(ctx context.Context) (model string, dim int, err error)
	UpdateEmbeddingConfig(ctx context.Context, model string, dim int) error
}

// pgxEmbeddingConfigDB implements EmbeddingConfigDB using a pgxpool.Pool.
type pgxEmbeddingConfigDB struct {
	pool *pgxpool.Pool
}

// NewPgxEmbeddingConfigDB wraps a pgxpool.Pool as an EmbeddingConfigDB.
func NewPgxEmbeddingConfigDB(pool *pgxpool.Pool) EmbeddingConfigDB {
	return &pgxEmbeddingConfigDB{pool: pool}
}

func (p *pgxEmbeddingConfigDB) GetEmbeddingConfig(ctx context.Context) (string, int, error) {
	var model string
	var dim int
	err := p.pool.QueryRow(ctx,
		`SELECT model_name, dimension FROM embedding_config WHERE id = TRUE`,
	).Scan(&model, &dim)
	if err != nil {
		return "", 0, fmt.Errorf("get embedding config: %w", err)
	}
	return model, dim, nil
}

func (p *pgxEmbeddingConfigDB) UpdateEmbeddingConfig(ctx context.Context, model string, dim int) error {
	_, err := p.pool.Exec(ctx,
		`UPDATE embedding_config SET model_name = $1, dimension = $2, updated_at = now() WHERE id = TRUE`,
		model, dim,
	)
	if err != nil {
		return fmt.Errorf("update embedding config: %w", err)
	}
	return nil
}

// ValidateEmbeddingConfig compares the DB's recorded embedding model/dimension
// against the values from the application config. Returns an error on mismatch,
// directing the operator to run 'openbrain reembed'.
func ValidateEmbeddingConfig(ctx context.Context, db EmbeddingConfigDB, cfgModel string, cfgDim int) error {
	dbModel, dbDim, err := db.GetEmbeddingConfig(ctx)
	if err != nil {
		return err
	}
	if dbModel != cfgModel || dbDim != cfgDim {
		return fmt.Errorf(
			"embedding config mismatch: env says %s/%d but DB has %s/%d — run 'openbrain reembed' to update",
			cfgModel, cfgDim, dbModel, dbDim,
		)
	}
	return nil
}
