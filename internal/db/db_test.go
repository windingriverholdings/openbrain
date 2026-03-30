package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVecLiteral_EmptySlice(t *testing.T) {
	// An empty slice produces "[]" which pgvector rejects.
	// Document current behavior — the guard should be in InsertThought.
	result := VecLiteral([]float32{})
	assert.Equal(t, "[]", result)
}

func TestVecLiteral_ValidSlice(t *testing.T) {
	result := VecLiteral([]float32{0.1, 0.2, 0.3})
	assert.Equal(t, "[0.1,0.2,0.3]", result)
}

func TestInsertThought_RejectsEmptyEmbedding(t *testing.T) {
	// InsertThought must return an error before hitting PostgreSQL
	// when given an empty embedding vector.
	_, err := InsertThought(
		nil, // ctx — won't reach DB
		nil, // pool — won't reach DB
		"test content",
		[]float32{},     // empty embedding
		"insight",       // thoughtType
		[]string{"tag"}, // tags
		"test",          // source
		nil,             // summary
		nil,             // metadata
	)

	assert.Error(t, err, "InsertThought must reject empty embeddings before hitting pgvector")
	assert.Contains(t, err.Error(), "empty embedding")
}

func TestInsertThought_RejectsNilEmbedding(t *testing.T) {
	_, err := InsertThought(
		nil,
		nil,
		"test content",
		nil,             // nil embedding
		"insight",
		[]string{"tag"},
		"test",
		nil,
		nil,
	)

	assert.Error(t, err, "InsertThought must reject nil embeddings before hitting pgvector")
	assert.Contains(t, err.Error(), "empty embedding")
}
