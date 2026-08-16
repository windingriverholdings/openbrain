package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/windingriverholdings/openbrain/internal/version"
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

func TestSearchSummaryModelFromEnv(t *testing.T) {
	t.Setenv("OPENBRAIN_SEARCH_SUMMARY_MODEL", "summary-model")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, "summary-model", cfg.SearchSummaryModel)
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
	t.Setenv("OPENBRAIN_MCP_AUTH_TOKEN", "abcdefghijklmnopqrstuvwxyz123456")
	t.Setenv("OPENBRAIN_OAUTH_ISSUER", "https://openbrain.example.com")
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

func TestMCPHTTPEnabled_EmptyToken_RunsOpen(t *testing.T) {
	t.Setenv("OPENBRAIN_MCP_HTTP_ENABLED", "true")
	// No token set: conditional posture allows open mode. No OAuth issuer is
	// required because the OAuth machinery does not mount in open mode.
	cfg, err := Load()
	assert.NoError(t, err)
	assert.True(t, cfg.MCPHTTPEnabled)
	assert.Empty(t, cfg.MCPAuthToken)
}

func TestMCPHTTPEnabled_RejectsShortToken(t *testing.T) {
	t.Setenv("OPENBRAIN_MCP_HTTP_ENABLED", "true")
	t.Setenv("OPENBRAIN_MCP_AUTH_TOKEN", "too-short")
	_, err := Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least 32 characters")
}

func TestMCPHTTPEnabled_Rejects31CharToken(t *testing.T) {
	t.Setenv("OPENBRAIN_MCP_HTTP_ENABLED", "true")
	t.Setenv("OPENBRAIN_MCP_AUTH_TOKEN", "abcdefghijklmnopqrstuvwxyz12345") // exactly 31
	_, err := Load()
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "at least 32 characters")
	}
}

func TestMCPHTTPEnabled_Accepts32CharToken(t *testing.T) {
	t.Setenv("OPENBRAIN_MCP_HTTP_ENABLED", "true")
	t.Setenv("OPENBRAIN_MCP_AUTH_TOKEN", "abcdefghijklmnopqrstuvwxyz123456")
	t.Setenv("OPENBRAIN_OAUTH_ISSUER", "https://openbrain.example.com")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.True(t, cfg.MCPHTTPEnabled)
	assert.Equal(t, "abcdefghijklmnopqrstuvwxyz123456", cfg.MCPAuthToken)
}

func TestMCPHTTPEnabled_RequiresOAuthIssuer(t *testing.T) {
	t.Setenv("OPENBRAIN_MCP_HTTP_ENABLED", "true")
	t.Setenv("OPENBRAIN_MCP_AUTH_TOKEN", "abcdefghijklmnopqrstuvwxyz123456")
	// No issuer set
	_, err := Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "OPENBRAIN_OAUTH_ISSUER is required")
}

func TestMCPHTTPEnabled_RejectsBareIssuer(t *testing.T) {
	t.Setenv("OPENBRAIN_MCP_HTTP_ENABLED", "true")
	t.Setenv("OPENBRAIN_MCP_AUTH_TOKEN", "abcdefghijklmnopqrstuvwxyz123456")
	t.Setenv("OPENBRAIN_OAUTH_ISSUER", "openbrain.example.com")
	_, err := Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "full URL")
}

func TestMCPHTTPDisabled_AllowsAnyToken(t *testing.T) {
	// When MCP HTTP is disabled, short/empty tokens are fine
	t.Setenv("OPENBRAIN_MCP_HTTP_ENABLED", "false")
	t.Setenv("OPENBRAIN_MCP_AUTH_TOKEN", "short")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.False(t, cfg.MCPHTTPEnabled)
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

func TestWebWSToken_EmptyAllowed(t *testing.T) {
	// Empty token is fine — WebSocket runs without auth
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Empty(t, cfg.WebWSToken)
}

func TestWebWSToken_RejectsShortToken(t *testing.T) {
	t.Setenv("OPENBRAIN_WEB_WS_TOKEN", "too-short")
	_, err := Load()
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "OPENBRAIN_WEB_WS_TOKEN")
		assert.Contains(t, err.Error(), "at least 32 characters")
	}
}

func TestWebWSToken_Rejects31CharToken(t *testing.T) {
	token := "abcdefghijklmnopqrstuvwxyz12345" // exactly 31, one under the minimum
	t.Setenv("OPENBRAIN_WEB_WS_TOKEN", token)
	_, err := Load()
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "at least 32 characters")
	}
}

func TestWebWSToken_Accepts32CharToken(t *testing.T) {
	token := "abcdefghijklmnopqrstuvwxyz123456" // exactly 32
	t.Setenv("OPENBRAIN_WEB_WS_TOKEN", token)
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, token, cfg.WebWSToken)
}

func TestWebWSToken_AcceptsLongToken(t *testing.T) {
	token := "abcdefghijklmnopqrstuvwxyz1234567890abcdef" // 42 chars
	t.Setenv("OPENBRAIN_WEB_WS_TOKEN", token)
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, token, cfg.WebWSToken)
}

// TestMCPServerVersion_DefaultsToVersionVar confirms that when
// OPENBRAIN_MCP_SERVER_VERSION is not set in the environment, MCPServerVersion
// falls back to version.Version (the canonical var in internal/version/version.go).
// In local dev builds that var is "dev"; in release builds @semantic-release/exec
// rewrites it to the computed semver (e.g. "0.3.0").
func TestMCPServerVersion_DefaultsToVersionVar(t *testing.T) {
	// Do not set OPENBRAIN_MCP_SERVER_VERSION: we want the default path.
	t.Setenv("OPENBRAIN_MCP_SERVER_VERSION", "")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, version.Version, cfg.MCPServerVersion,
		"MCPServerVersion must equal version.Version when env var is absent")
}

// TestMCPServerVersion_EnvOverridesVersionVar confirms the env var
// OPENBRAIN_MCP_SERVER_VERSION takes precedence over version.Version.
func TestMCPServerVersion_EnvOverridesVersionVar(t *testing.T) {
	t.Setenv("OPENBRAIN_MCP_SERVER_VERSION", "9.9.9")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, "9.9.9", cfg.MCPServerVersion,
		"OPENBRAIN_MCP_SERVER_VERSION env var must override version.Version")
}

// TestVizTTL_DefaultsTo24h confirms an unset OPENBRAIN_VIZ_TTL resolves to
// the 24h default, not zero (which would mean "staleness disabled").
func TestVizTTL_DefaultsTo24h(t *testing.T) {
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, 24*time.Hour, cfg.VizTTL)
}

// TestVizTTL_EmptyDisablesStaleness is the item this hand-parsed field
// exists for: caarlos0/env's tag-based defaulting would collapse an
// explicitly-empty env var into the envDefault, making this indistinguishable
// from unset. parseVizTTL must not do that.
func TestVizTTL_EmptyDisablesStaleness(t *testing.T) {
	t.Setenv("OPENBRAIN_VIZ_TTL", "")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Zero(t, cfg.VizTTL)
}

func TestVizTTL_ZeroDisablesStaleness(t *testing.T) {
	t.Setenv("OPENBRAIN_VIZ_TTL", "0")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Zero(t, cfg.VizTTL)
}

func TestVizTTL_CustomValueFromEnv(t *testing.T) {
	t.Setenv("OPENBRAIN_VIZ_TTL", "1h")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, time.Hour, cfg.VizTTL)
}

func TestVizTTL_MalformedValue_FailsLoad(t *testing.T) {
	t.Setenv("OPENBRAIN_VIZ_TTL", "not-a-duration")
	_, err := Load()
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "OPENBRAIN_VIZ_TTL")
	}
}

func TestVizTTL_NegativeValue_FailsLoad(t *testing.T) {
	t.Setenv("OPENBRAIN_VIZ_TTL", "-1h")
	_, err := Load()
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "OPENBRAIN_VIZ_TTL")
	}
}

// TestVizPythonPath_DefaultsToPython3 confirms an unset OPENBRAIN_VIZ_PYTHON
// resolves to "python3", so the rebuild pipeline's behavior is unchanged for
// every install that hasn't provisioned a dedicated venv yet.
func TestVizPythonPath_DefaultsToPython3(t *testing.T) {
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, "python3", cfg.VizPythonPath)
}

// TestVizPythonPath_FromEnv confirms OPENBRAIN_VIZ_PYTHON overrides the
// default, e.g. to point at a venv python with the viz deps installed.
func TestVizPythonPath_FromEnv(t *testing.T) {
	t.Setenv("OPENBRAIN_VIZ_PYTHON", "/opt/openbrain-viz-venv/bin/python3")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, "/opt/openbrain-viz-venv/bin/python3", cfg.VizPythonPath)
}

// TestVizPythonInterpreter_FallsBackToPython3WhenFieldEmpty confirms the
// accessor's own fallback (not just the envDefault tag) so a Config built
// directly, bypassing Load, still gets the zero-behavior-change default.
func TestVizPythonInterpreter_FallsBackToPython3WhenFieldEmpty(t *testing.T) {
	cfg := &Config{}
	assert.Equal(t, "python3", cfg.VizPythonInterpreter())
}

// TestVizPythonInterpreter_ReturnsConfiguredValue confirms a set
// VizPythonPath flows straight through the accessor unchanged.
func TestVizPythonInterpreter_ReturnsConfiguredValue(t *testing.T) {
	cfg := &Config{VizPythonPath: "/opt/openbrain-viz-venv/bin/python3"}
	assert.Equal(t, "/opt/openbrain-viz-venv/bin/python3", cfg.VizPythonInterpreter())
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

// TestRetrievalDepthDefaults pins the shipped retrieval depths. Assisted search
// deliberately retrieves deeper than a plain search because it fuses several
// ranked lists, and the summary cap is lower than the search cap so a deep
// search cannot overflow the local model's context window.
func TestRetrievalDepthDefaults(t *testing.T) {
	cfg, err := Load()
	assert.NoError(t, err)

	assert.Equal(t, 10, cfg.SearchTopK)
	assert.Equal(t, 25, cfg.SearchAssistedTopK)
	assert.Equal(t, 10, cfg.SearchProbeTopK)
	assert.Equal(t, 60, cfg.SearchRRFK)
	assert.Equal(t, 15, cfg.SearchSummaryTopK)
	assert.LessOrEqual(t, cfg.SearchSummaryTopK, cfg.SearchAssistedTopK,
		"the summary must never be asked to read more rows than search returns")
}

// TestRetrievalDepthOverridesFromEnv asserts each retrieval knob is tunable
// without a rebuild.
func TestRetrievalDepthOverridesFromEnv(t *testing.T) {
	t.Setenv("OPENBRAIN_SEARCH_ASSISTED_TOP_K", "40")
	t.Setenv("OPENBRAIN_SEARCH_PROBE_TOP_K", "12")
	t.Setenv("OPENBRAIN_SEARCH_RRF_K", "90")
	t.Setenv("OPENBRAIN_SEARCH_SUMMARY_TOP_K", "20")

	cfg, err := Load()
	assert.NoError(t, err)

	assert.Equal(t, 40, cfg.SearchAssistedTopK)
	assert.Equal(t, 12, cfg.SearchProbeTopK)
	assert.Equal(t, 90, cfg.SearchRRFK)
	assert.Equal(t, 20, cfg.SearchSummaryTopK)
}

func TestConverseDefaults(t *testing.T) {
	cfg, err := Load()
	assert.NoError(t, err)

	assert.Equal(t, "", cfg.ConverseModel)
	assert.Equal(t, "", cfg.ConverseRewriteModel)
	assert.Equal(t, 8, cfg.ConverseMaxNotes)
	assert.Equal(t, 3000, cfg.ConversePromptTokens)
	assert.Equal(t, 8192, cfg.ConverseNumCtx)
	assert.Equal(t, 1, cfg.ConverseMaxRounds)
	assert.Equal(t, 30*time.Minute, cfg.ConverseKeepAlive)
	assert.Equal(t, 25*time.Second, cfg.ConverseRewriteTimeout)
	assert.Equal(t, 90*time.Second, cfg.ConverseAnswerTimeout)
	assert.False(t, cfg.ConverseWarmModel)
}

func TestConverseOverridesFromEnv(t *testing.T) {
	t.Setenv("OPENBRAIN_CONVERSE_MODEL", "qwen2.5:7b")
	t.Setenv("OPENBRAIN_CONVERSE_REWRITE_MODEL", "qwen2.5:1.5b")
	t.Setenv("OPENBRAIN_CONVERSE_MAX_NOTES", "12")
	t.Setenv("OPENBRAIN_CONVERSE_PROMPT_TOKENS", "4000")
	t.Setenv("OPENBRAIN_CONVERSE_NUM_CTX", "16384")
	t.Setenv("OPENBRAIN_CONVERSE_MAX_ROUNDS", "2")
	t.Setenv("OPENBRAIN_CONVERSE_KEEP_ALIVE", "15m")
	t.Setenv("OPENBRAIN_CONVERSE_REWRITE_TIMEOUT", "10s")
	t.Setenv("OPENBRAIN_CONVERSE_ANSWER_TIMEOUT", "45s")
	t.Setenv("OPENBRAIN_CONVERSE_WARM_MODEL", "true")

	cfg, err := Load()
	assert.NoError(t, err)

	assert.Equal(t, "qwen2.5:7b", cfg.ConverseModel)
	assert.Equal(t, "qwen2.5:1.5b", cfg.ConverseRewriteModel)
	assert.Equal(t, 12, cfg.ConverseMaxNotes)
	assert.Equal(t, 4000, cfg.ConversePromptTokens)
	assert.Equal(t, 16384, cfg.ConverseNumCtx)
	assert.Equal(t, 2, cfg.ConverseMaxRounds)
	assert.Equal(t, 15*time.Minute, cfg.ConverseKeepAlive)
	assert.Equal(t, 10*time.Second, cfg.ConverseRewriteTimeout)
	assert.Equal(t, 45*time.Second, cfg.ConverseAnswerTimeout)
	assert.True(t, cfg.ConverseWarmModel)
}
