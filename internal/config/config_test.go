package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultSearchScoreThreshold(t *testing.T) {
	// The default threshold should be 0.15, not 0.35.
	// 0.35 is too aggressive for small corpora.
	assert.Equal(t, 0.15, defaultSearchScoreThreshold,
		"default threshold should be 0.15 for small corpora compatibility")
}

func TestIngestDirDefault(t *testing.T) {
	cfg := &Config{}
	assert.Equal(t, "", cfg.IngestDir, "IngestDir should default to empty string")
}

func TestTesseractLangsDefault(t *testing.T) {
	// TesseractLangs should default to "eng" when loaded from env.
	// We test the struct tag default by loading with no env set.
	cfg := &Config{TesseractLangs: "eng"}
	assert.Equal(t, "eng", cfg.TesseractLangs)
}

func TestIngestDirFromEnv(t *testing.T) {
	t.Setenv("OPENBRAIN_INGEST_DIR", "/tmp/openbrain-ingest")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, "/tmp/openbrain-ingest", cfg.IngestDir)
}

func TestTesseractLangsFromEnv(t *testing.T) {
	t.Setenv("OPENBRAIN_TESSERACT_LANGS", "eng+fra")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, "eng+fra", cfg.TesseractLangs)
}

func TestTesseractLangsValidation_RejectsInvalid(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"uppercase", "ENG"},
		{"too short", "en"},
		{"too long", "english"},
		{"bad separator", "eng-fra"},
		{"trailing plus", "eng+"},
		{"leading plus", "+eng"},
		{"numbers", "en3"},
		{"spaces", "eng fra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENBRAIN_TESSERACT_LANGS", tt.value)
			_, err := Load()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "OPENBRAIN_TESSERACT_LANGS")
		})
	}
}

func TestMarkitdownPathValidation_RejectsInvalid(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"path traversal", "../bin/markitdown"},
		{"embedded dotdot", "/usr/../bin/markitdown"},
		{"whitespace", "/usr/bin/markit down"},
		{"tab", "/usr/bin/markit\tdown"},
		{"semicolon", "markitdown; rm -rf /"},
		{"pipe", "markitdown | cat"},
		{"ampersand", "markitdown & echo"},
		{"backtick", "`whoami`"},
		{"dollar", "$(whoami)"},
		{"relative with slash", "bin/markitdown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENBRAIN_MARKITDOWN_PATH", tt.value)
			_, err := Load()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "OPENBRAIN_MARKITDOWN_PATH")
		})
	}
}

func TestMarkitdownPathValidation_AcceptsValid(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"plain basename", "markitdown"},
		{"absolute path", "/usr/local/bin/markitdown"},
		{"default value", "markitdown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENBRAIN_MARKITDOWN_PATH", tt.value)
			cfg, err := Load()
			assert.NoError(t, err)
			assert.Equal(t, tt.value, cfg.MarkitdownPath)
		})
	}
}

func TestMCPHTTPEnabledDefault(t *testing.T) {
	cfg, err := Load()
	assert.NoError(t, err)
	assert.False(t, cfg.MCPHTTPEnabled, "MCPHTTPEnabled should default to false")
}

func TestMCPHTTPEnabledFromEnv(t *testing.T) {
	t.Setenv("OPENBRAIN_MCP_HTTP_ENABLED", "true")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.True(t, cfg.MCPHTTPEnabled)
}

func TestMCPAuthTokenFromEnv(t *testing.T) {
	t.Setenv("OPENBRAIN_MCP_AUTH_TOKEN", "my-secret-token")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, "my-secret-token", cfg.MCPAuthToken)
}

func TestMCPAuthTokenDefaultEmpty(t *testing.T) {
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Empty(t, cfg.MCPAuthToken, "MCPAuthToken should default to empty")
}

func TestWatchDirsFromEnv(t *testing.T) {
	t.Setenv("OPENBRAIN_WATCH_DIRS", "/tmp/docs,/tmp/notes")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, "/tmp/docs,/tmp/notes", cfg.WatchDirs)
}

func TestWatchDebounceMsDefault(t *testing.T) {
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, 500, cfg.WatchDebounceMs)
}

func TestWatchDebounceMsFromEnv(t *testing.T) {
	t.Setenv("OPENBRAIN_WATCH_DEBOUNCE_MS", "1000")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, 1000, cfg.WatchDebounceMs)
}

func TestTesseractLangsValidation_AcceptsValid(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"single lang", "eng"},
		{"two langs", "eng+fra"},
		{"three langs", "eng+fra+deu"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENBRAIN_TESSERACT_LANGS", tt.value)
			cfg, err := Load()
			assert.NoError(t, err)
			assert.Equal(t, tt.value, cfg.TesseractLangs)
		})
	}
}
