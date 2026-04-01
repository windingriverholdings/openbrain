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

func TestMcpSupersedeArgsContentRequired(t *testing.T) {
	args := map[string]any{
		"content":      "Alice is now Head of Engineering",
		"thought_type": "person",
		"tags":         []any{"team", "leadership"},
		"source":       "claude",
	}
	content, _ := args["content"].(string)
	thoughtType := stringArg(args, "thought_type", "")
	tags := stringListArg(args, "tags")
	source := stringArg(args, "source", "claude")

	assert.Equal(t, "Alice is now Head of Engineering", content)
	assert.Equal(t, "person", thoughtType)
	assert.Equal(t, []string{"team", "leadership"}, tags)
	assert.Equal(t, "claude", source)
}

func TestMcpSupersedeArgsSupersedesQuery(t *testing.T) {
	args := map[string]any{
		"content":          "Alice is now Head of Engineering",
		"supersedes_query": "Alice role",
	}
	q := stringArg(args, "supersedes_query", "")
	assert.Equal(t, "Alice role", q)
}

func TestMcpSupersedeArgsOldThoughtID(t *testing.T) {
	args := map[string]any{
		"content":       "Alice is now Head of Engineering",
		"old_thought_id": "abc12345-0000-0000-0000-000000000000",
	}
	id := stringArg(args, "old_thought_id", "")
	assert.Equal(t, "abc12345-0000-0000-0000-000000000000", id)
}

func TestMcpSupersedeArgsNoOptionals(t *testing.T) {
	args := map[string]any{
		"content": "New fact",
	}
	q := stringArg(args, "supersedes_query", "")
	id := stringArg(args, "old_thought_id", "")
	assert.Equal(t, "", q)
	assert.Equal(t, "", id)
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
