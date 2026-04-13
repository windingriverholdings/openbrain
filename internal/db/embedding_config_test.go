package db

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEmbeddingConfigDB implements EmbeddingConfigDB for testing.
type mockEmbeddingConfigDB struct {
	model    string
	dim      int
	getErr   error
	updateFn func(model string, dim int) error
}

func (m *mockEmbeddingConfigDB) GetEmbeddingConfig(ctx context.Context) (string, int, error) {
	if m.getErr != nil {
		return "", 0, m.getErr
	}
	return m.model, m.dim, nil
}

func (m *mockEmbeddingConfigDB) UpdateEmbeddingConfig(ctx context.Context, model string, dim int) error {
	if m.updateFn != nil {
		return m.updateFn(model, dim)
	}
	m.model = model
	m.dim = dim
	return nil
}

var _ EmbeddingConfigDB = (*mockEmbeddingConfigDB)(nil)

// --- ValidateEmbeddingConfig tests ---

func TestValidateEmbeddingConfig_Match(t *testing.T) {
	db := &mockEmbeddingConfigDB{model: "nomic-embed-text", dim: 768}
	err := ValidateEmbeddingConfig(context.Background(), db, "nomic-embed-text", 768)
	require.NoError(t, err)
}

func TestValidateEmbeddingConfig_ModelMismatch(t *testing.T) {
	db := &mockEmbeddingConfigDB{model: "nomic-embed-text", dim: 768}
	err := ValidateEmbeddingConfig(context.Background(), db, "all-minilm", 768)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mismatch")
}

func TestValidateEmbeddingConfig_DimMismatch(t *testing.T) {
	db := &mockEmbeddingConfigDB{model: "nomic-embed-text", dim: 768}
	err := ValidateEmbeddingConfig(context.Background(), db, "nomic-embed-text", 384)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mismatch")
}

func TestValidateEmbeddingConfig_BothMismatch(t *testing.T) {
	db := &mockEmbeddingConfigDB{model: "nomic-embed-text", dim: 768}
	err := ValidateEmbeddingConfig(context.Background(), db, "all-minilm", 384)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mismatch")
}

func TestValidateEmbeddingConfig_DBError(t *testing.T) {
	db := &mockEmbeddingConfigDB{getErr: fmt.Errorf("connection refused")}
	err := ValidateEmbeddingConfig(context.Background(), db, "nomic-embed-text", 768)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

// --- UpdateEmbeddingConfig via mock ---

func TestUpdateEmbeddingConfig_Success(t *testing.T) {
	var calledModel string
	var calledDim int
	db := &mockEmbeddingConfigDB{
		updateFn: func(model string, dim int) error {
			calledModel = model
			calledDim = dim
			return nil
		},
	}
	err := db.UpdateEmbeddingConfig(context.Background(), "nomic-embed-text", 768)
	require.NoError(t, err)
	assert.Equal(t, "nomic-embed-text", calledModel)
	assert.Equal(t, 768, calledDim)
}

func TestUpdateEmbeddingConfig_Error(t *testing.T) {
	db := &mockEmbeddingConfigDB{
		updateFn: func(model string, dim int) error {
			return fmt.Errorf("update failed")
		},
	}
	err := db.UpdateEmbeddingConfig(context.Background(), "nomic-embed-text", 768)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update failed")
}

func TestUpdateEmbeddingConfig_RowNotFound(t *testing.T) {
	// Simulates the zero-rows-affected case from pgxEmbeddingConfigDB:
	// when migration 009 hasn't been applied, the UPDATE matches no rows.
	db := &mockEmbeddingConfigDB{
		updateFn: func(model string, dim int) error {
			return fmt.Errorf("embedding_config row not found — ensure migration 009 has been applied")
		},
	}
	err := db.UpdateEmbeddingConfig(context.Background(), "nomic-embed-text", 768)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "row not found")
	assert.Contains(t, err.Error(), "migration 009")
}
