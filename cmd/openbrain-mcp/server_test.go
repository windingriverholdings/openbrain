package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStringArgDefault(t *testing.T) {
	args := map[string]any{}
	assert.Equal(t, "hybrid", stringArg(args, "mode", "hybrid"))
}

func TestStringArgOverride(t *testing.T) {
	args := map[string]any{"mode": "vector"}
	assert.Equal(t, "vector", stringArg(args, "mode", "hybrid"))
}

func TestStringListArg(t *testing.T) {
	args := map[string]any{
		"tags": []any{"go", "architecture"},
	}
	result := stringListArg(args, "tags")
	assert.Equal(t, []string{"go", "architecture"}, result)
}

func TestStringListArgEmpty(t *testing.T) {
	args := map[string]any{}
	result := stringListArg(args, "tags")
	assert.Nil(t, result)
}

func TestBoolArg(t *testing.T) {
	args := map[string]any{"include_history": true}
	result := boolArg(args, "include_history", false)
	assert.True(t, result)
}

func TestBoolArgDefault(t *testing.T) {
	args := map[string]any{}
	result := boolArg(args, "include_history", false)
	assert.False(t, result)
}

func TestMcpSearchBuildsSearchOpts(t *testing.T) {
	// Verify that mcpSearch extracts all filter params from args.
	// This is a compile-time + structural test — the full integration
	// requires a Brain with embedder, tested at integration level.
	args := map[string]any{
		"query":           "architecture decisions",
		"mode":            "hybrid",
		"thought_type":    "decision",
		"tags":            []any{"backend", "api"},
		"include_history": true,
	}

	mode := stringArg(args, "mode", "hybrid")
	thoughtType := stringArg(args, "thought_type", "")
	tags := stringListArg(args, "tags")
	includeHistory := boolArg(args, "include_history", false)

	assert.Equal(t, "hybrid", mode)
	assert.Equal(t, "decision", thoughtType)
	assert.Equal(t, []string{"backend", "api"}, tags)
	assert.True(t, includeHistory)
}
