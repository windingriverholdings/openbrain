// Package sql_test validates the 009 migration SQL.
package sql_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration009_FileExists(t *testing.T) {
	_, err := os.Stat("009_embedding_config.sql")
	require.NoError(t, err, "sql/009_embedding_config.sql must exist")
}

func TestMigration009_CreatesEmbeddingConfigTable(t *testing.T) {
	data, err := os.ReadFile("009_embedding_config.sql")
	require.NoError(t, err)
	sql := strings.ToUpper(string(data))

	assert.Contains(t, sql, "CREATE TABLE",
		"migration must CREATE TABLE embedding_config")
	assert.Contains(t, sql, "EMBEDDING_CONFIG",
		"migration must reference embedding_config table")
}

func TestMigration009_HasCheckConstraint(t *testing.T) {
	data, err := os.ReadFile("009_embedding_config.sql")
	require.NoError(t, err)
	sql := strings.ToUpper(string(data))

	assert.Contains(t, sql, "CHECK",
		"migration must include CHECK constraint for singleton row")
}

func TestMigration009_HasOnConflictDoNothing(t *testing.T) {
	data, err := os.ReadFile("009_embedding_config.sql")
	require.NoError(t, err)
	sql := strings.ToUpper(string(data))

	assert.Contains(t, sql, "ON CONFLICT",
		"migration must use ON CONFLICT for idempotent seed")
	assert.Contains(t, sql, "DO NOTHING",
		"migration must use DO NOTHING to preserve existing config")
}

func TestMigration009_SeedsNomicEmbedText(t *testing.T) {
	data, err := os.ReadFile("009_embedding_config.sql")
	require.NoError(t, err)
	sql := string(data)

	assert.Contains(t, sql, "nomic-embed-text",
		"migration must seed nomic-embed-text as the default model")
}

func TestMigration009_Seeds768(t *testing.T) {
	data, err := os.ReadFile("009_embedding_config.sql")
	require.NoError(t, err)
	sql := string(data)

	assert.Contains(t, sql, "768",
		"migration must seed 768 as the default dimension")
}
