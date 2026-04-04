package mcptools

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

func TestSupersedeArgsContentRequired(t *testing.T) {
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

func TestSupersedeArgsSupersedesQuery(t *testing.T) {
	args := map[string]any{
		"content":          "Alice is now Head of Engineering",
		"supersedes_query": "Alice role",
	}
	q := stringArg(args, "supersedes_query", "")
	assert.Equal(t, "Alice role", q)
}

func TestSupersedeArgsOldThoughtID(t *testing.T) {
	args := map[string]any{
		"content":        "Alice is now Head of Engineering",
		"old_thought_id": "abc12345-0000-0000-0000-000000000000",
	}
	id := stringArg(args, "old_thought_id", "")
	assert.Equal(t, "abc12345-0000-0000-0000-000000000000", id)
}

func TestSupersedeArgsNoOptionals(t *testing.T) {
	args := map[string]any{
		"content": "New fact",
	}
	q := stringArg(args, "supersedes_query", "")
	id := stringArg(args, "old_thought_id", "")
	assert.Equal(t, "", q)
	assert.Equal(t, "", id)
}

func TestSearchBuildsSearchOpts(t *testing.T) {
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

func TestSanitizeIngestError_NoPathLeakage(t *testing.T) {
	tests := []struct {
		name     string
		err      string
		contains string
		absent   string
	}{
		{
			"removes internal path",
			"open /home/user/secret/data.pdf: no such file",
			"file not found",
			"/home/user/secret",
		},
		{
			"generic error passthrough",
			"unsupported format",
			"unsupported format",
			"",
		},
		{
			"file too large sanitized",
			"file too large: 100000000 bytes exceeds limit of 52428800 bytes",
			"file too large",
			"100000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sanitized := sanitizeIngestError(tt.err)
			assert.Contains(t, sanitized, tt.contains)
			if tt.absent != "" {
				assert.NotContains(t, sanitized, tt.absent)
			}
		})
	}
}

func TestSourceMaxLen(t *testing.T) {
	assert.Equal(t, 255, sourceMaxLen)
}
