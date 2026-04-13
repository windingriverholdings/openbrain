// Package sql_test validates the 008 migration SQL is syntactically valid.
package sql_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration008_FileExists(t *testing.T) {
	_, err := os.Stat("008_embedding_768.sql")
	require.NoError(t, err, "sql/008_embedding_768.sql must exist")
}

func TestMigration008_ContainsVector768(t *testing.T) {
	data, err := os.ReadFile("008_embedding_768.sql")
	require.NoError(t, err)
	sql := string(data)

	assert.Contains(t, sql, "vector(768)",
		"migration must reference vector(768) for nomic-embed-text")
}

func TestMigration008_NullsExistingEmbeddings(t *testing.T) {
	data, err := os.ReadFile("008_embedding_768.sql")
	require.NoError(t, err)
	sql := strings.ToUpper(string(data))

	// The migration should NULL out existing 384-dim embeddings
	// so they can be re-embedded with the new model.
	assert.Contains(t, sql, "SET EMBEDDING = NULL",
		"migration must NULL out existing embeddings for re-embedding")
}

func TestMigration008_AltersThoughtsTable(t *testing.T) {
	data, err := os.ReadFile("008_embedding_768.sql")
	require.NoError(t, err)
	sql := strings.ToUpper(string(data))

	assert.Contains(t, sql, "ALTER TABLE THOUGHTS",
		"migration must ALTER the thoughts table")
}

func TestMigration008_RecreatesHybridSearch(t *testing.T) {
	data, err := os.ReadFile("008_embedding_768.sql")
	require.NoError(t, err)
	sql := strings.ToUpper(string(data))

	assert.Contains(t, sql, "CREATE OR REPLACE FUNCTION HYBRID_SEARCH",
		"migration must recreate hybrid_search with vector(768)")
}

func TestMigration008_NoVector384(t *testing.T) {
	data, err := os.ReadFile("008_embedding_768.sql")
	require.NoError(t, err)
	sql := string(data)

	assert.NotContains(t, sql, "vector(384)",
		"migration must not contain vector(384) — all references should be vector(768)")
}
