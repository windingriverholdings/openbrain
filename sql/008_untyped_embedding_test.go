// Package sql_test validates the 008 migration SQL is syntactically valid.
package sql_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration008_FileExists(t *testing.T) {
	_, err := os.Stat("008_untyped_embedding.sql")
	require.NoError(t, err, "sql/008_untyped_embedding.sql must exist")
}

func TestMigration008_UsesUntypedVector(t *testing.T) {
	data, err := os.ReadFile("008_untyped_embedding.sql")
	require.NoError(t, err)
	sql := string(data)

	// Must contain untyped "vector" references (ALTER and function param)
	assert.Contains(t, sql, "TYPE vector",
		"migration must ALTER column to untyped vector")
	assert.Contains(t, sql, "query_embedding vector,",
		"hybrid_search must use untyped vector parameter")
}

func TestMigration008_NullsExistingEmbeddings(t *testing.T) {
	data, err := os.ReadFile("008_untyped_embedding.sql")
	require.NoError(t, err)
	sql := strings.ToUpper(string(data))

	// The migration should NULL out existing embeddings
	// so they can be re-embedded with the active model.
	assert.Contains(t, sql, "SET EMBEDDING = NULL",
		"migration must NULL out existing embeddings for re-embedding")
}

func TestMigration008_AltersThoughtsTable(t *testing.T) {
	data, err := os.ReadFile("008_untyped_embedding.sql")
	require.NoError(t, err)
	sql := strings.ToUpper(string(data))

	assert.Contains(t, sql, "ALTER TABLE THOUGHTS",
		"migration must ALTER the thoughts table")
}

func TestMigration008_RecreatesHybridSearch(t *testing.T) {
	data, err := os.ReadFile("008_untyped_embedding.sql")
	require.NoError(t, err)
	sql := strings.ToUpper(string(data))

	assert.Contains(t, sql, "CREATE OR REPLACE FUNCTION HYBRID_SEARCH",
		"migration must recreate hybrid_search function")
}

func TestMigration008_NoDimensionConstrainedVector(t *testing.T) {
	data, err := os.ReadFile("008_untyped_embedding.sql")
	require.NoError(t, err)
	sql := string(data)

	// No vector(N) should appear — the whole point is untyped/unconstrained vectors.
	// Only check non-comment lines to allow comments mentioning old dimensions.
	re := regexp.MustCompile(`vector\(\d+\)`)
	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue // skip SQL comments
		}
		assert.False(t, re.MatchString(line),
			"migration must not contain dimension-constrained vector types in SQL statements, found in: %s", line)
	}
}
