// Package config loads application settings from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"

	"github.com/windingriverholdings/openbrain/internal/version"
)

// tesseractLangsPattern validates TesseractLangs as one or more 3-letter
// ISO 639-2 codes separated by plus signs (e.g. "eng", "eng+fra+deu").
var tesseractLangsPattern = regexp.MustCompile(`^[a-z]{3}(\+[a-z]{3})*$`)

// markitdownPathPattern validates MarkitdownPath as either a plain basename
// (no path separators) or an absolute path. Rejects shell metacharacters,
// whitespace, and path traversal.
var markitdownPathPattern = regexp.MustCompile(`^(/[A-Za-z0-9._/-]+|[A-Za-z0-9._-]+)$`)

// DefaultIngestMaxBytes is the fallback file-size limit (50 MB) used when no
// explicit value is configured. Exported so both docparse and brain can share
// a single constant instead of duplicating the magic number.
const DefaultIngestMaxBytes int64 = 50 * 1024 * 1024

// defaultSearchScoreThreshold is the baseline min-score for search results.
// Lowered from 0.35 to 0.15 to avoid filtering out valid matches in small corpora.
const defaultSearchScoreThreshold = 0.15

// Config holds all application settings, loaded from environment variables
// with the OPENBRAIN_ prefix.
type Config struct {
	// Database
	DBHost     string `env:"OPENBRAIN_DB_HOST" envDefault:"localhost"`
	DBPort     int    `env:"OPENBRAIN_DB_PORT" envDefault:"5432"`
	DBName     string `env:"OPENBRAIN_DB_NAME" envDefault:"openbrain"`
	DBUser     string `env:"OPENBRAIN_DB_USER" envDefault:"openbrain"`
	DBPassword string `env:"OPENBRAIN_DB_PASSWORD,required,notEmpty"`

	// Embedding
	EmbeddingModel string `env:"OPENBRAIN_EMBEDDING_MODEL" envDefault:"nomic-embed-text"`
	EmbeddingDim   int    `env:"OPENBRAIN_EMBEDDING_DIM" envDefault:"768"`

	// MCP
	MCPServerName string `env:"OPENBRAIN_MCP_SERVER_NAME" envDefault:"openbrain"`
	// MCPServerVersion defaults to version.Version (the value of the canonical
	// var in internal/version/version.go) when OPENBRAIN_MCP_SERVER_VERSION is
	// not set in the environment. This allows @semantic-release/exec to rewrite
	// the var at release time and have all binaries pick it up automatically.
	// The env var override is preserved for environments that pin a version
	// externally (e.g. k8s ConfigMap, systemd EnvironmentFile).
	MCPServerVersion string `env:"OPENBRAIN_MCP_SERVER_VERSION"`
	MCPHTTPEnabled   bool   `env:"OPENBRAIN_MCP_HTTP_ENABLED" envDefault:"false"`
	MCPAuthToken     string `env:"OPENBRAIN_MCP_AUTH_TOKEN"`
	// MCPAllowedHosts is a comma-separated Host-header allowlist for the /mcp
	// and /sse transports (see internal/mcphttp.AllowedHosts). Loopback hosts
	// (localhost, 127.0.0.1, ::1) are always permitted in addition to this
	// list, so the default only needs to name the public host cloudflared
	// forwards for the deployed instance. Override for a different deployment.
	MCPAllowedHosts string `env:"OPENBRAIN_MCP_ALLOWED_HOSTS" envDefault:"openbrain.wr-s.net"`

	// OAuth (for Claude.ai MCP connector)
	OAuthClientID     string `env:"OPENBRAIN_OAUTH_CLIENT_ID"`
	OAuthClientSecret string `env:"OPENBRAIN_OAUTH_CLIENT_SECRET"`
	OAuthIssuer       string `env:"OPENBRAIN_OAUTH_ISSUER"`

	// Retrieval
	SearchTopK              int           `env:"OPENBRAIN_SEARCH_TOP_K" envDefault:"10"`
	SearchFastTopK          int           `env:"OPENBRAIN_SEARCH_FAST_TOP_K" envDefault:"20"`
	SearchAssistedTopK      int           `env:"OPENBRAIN_SEARCH_ASSISTED_TOP_K" envDefault:"25"`
	SearchProbeTopK         int           `env:"OPENBRAIN_SEARCH_PROBE_TOP_K" envDefault:"10"`
	SearchRRFK              int           `env:"OPENBRAIN_SEARCH_RRF_K" envDefault:"60"`
	SearchSummaryTopK       int           `env:"OPENBRAIN_SEARCH_SUMMARY_TOP_K" envDefault:"15"`
	SearchScoreThreshold    float64       `env:"OPENBRAIN_SEARCH_SCORE_THRESHOLD" envDefault:"0.15"`
	SearchFilteredThreshold float64       `env:"OPENBRAIN_SEARCH_FILTERED_THRESHOLD" envDefault:"0.01"`
	SearchAssistedModel     string        `env:"OPENBRAIN_SEARCH_ASSISTED_MODEL"`
	SearchSummaryModel      string        `env:"OPENBRAIN_SEARCH_SUMMARY_MODEL"`
	SearchAgentTimeout      time.Duration `env:"OPENBRAIN_SEARCH_AGENT_TIMEOUT" envDefault:"30s"`

	// Telegram
	TelegramBotToken      string `env:"OPENBRAIN_TELEGRAM_BOT_TOKEN"`
	TelegramAllowedUserID int64  `env:"OPENBRAIN_TELEGRAM_ALLOWED_USER_ID"`

	// Slack
	SlackBotToken      string `env:"OPENBRAIN_SLACK_BOT_TOKEN"`
	SlackAppToken      string `env:"OPENBRAIN_SLACK_APP_TOKEN"`
	SlackAllowedUserID string `env:"OPENBRAIN_SLACK_ALLOWED_USER_ID"`

	// Web UI
	WebHost           string `env:"OPENBRAIN_WEB_HOST" envDefault:"127.0.0.1"`
	WebPort           int    `env:"OPENBRAIN_WEB_PORT" envDefault:"10203"`
	WebAllowedOrigins string `env:"OPENBRAIN_WEB_ALLOWED_ORIGINS"` // comma-separated list of allowed WebSocket origins
	WebWSToken        string `env:"OPENBRAIN_WEB_WS_TOKEN"`        // optional auth token for /ws; when empty, WebSocket is open

	// Document ingestion
	IngestDir          string `env:"OPENBRAIN_INGEST_DIR"`
	IngestMaxBytes     int64  `env:"OPENBRAIN_INGEST_MAX_BYTES" envDefault:"52428800"` // 50 MB
	IngestChunkSize    int    `env:"OPENBRAIN_INGEST_CHUNK_SIZE" envDefault:"2000"`
	IngestChunkOverlap int    `env:"OPENBRAIN_INGEST_CHUNK_OVERLAP" envDefault:"200"`
	TesseractLangs     string `env:"OPENBRAIN_TESSERACT_LANGS" envDefault:"eng"`

	// Bulk import
	// BulkImportMaxItems caps the number of thoughts a single bulk_import call
	// will accept, so an unbounded batch cannot drive one embedder round-trip
	// per item and hold a single write transaction open indefinitely.
	BulkImportMaxItems int `env:"OPENBRAIN_BULK_IMPORT_MAX_ITEMS" envDefault:"500"`
	// BulkImportMaxContentChars caps the rune length of a single bulk_import
	// item's content, mirroring the ingest path's own size limits.
	BulkImportMaxContentChars int `env:"OPENBRAIN_BULK_IMPORT_MAX_CONTENT_CHARS" envDefault:"10000"`

	// LLM extraction
	ExtractProvider      string `env:"OPENBRAIN_EXTRACT_PROVIDER" envDefault:"none"`
	ExtractModel         string `env:"OPENBRAIN_EXTRACT_MODEL" envDefault:"gemma3"`
	ExtractModelFast     string `env:"OPENBRAIN_EXTRACT_MODEL_FAST"`
	ExtractFastThreshold int    `env:"OPENBRAIN_EXTRACT_FAST_THRESHOLD" envDefault:"500"`
	OllamaBaseURL        string `env:"OPENBRAIN_OLLAMA_BASE_URL" envDefault:"http://localhost:11434"`
	AnthropicAPIKey      string `env:"OPENBRAIN_ANTHROPIC_API_KEY"`

	// Folder watcher
	WatchDirs       string `env:"OPENBRAIN_WATCH_DIRS"`
	WatchDebounceMs int    `env:"OPENBRAIN_WATCH_DEBOUNCE_MS" envDefault:"500"`
	WatchStateFile  string `env:"OPENBRAIN_WATCH_STATE_FILE"`

	// External tool paths
	MarkitdownPath string `env:"OPENBRAIN_MARKITDOWN_PATH" envDefault:"markitdown"`

	// Brain visualization
	VizScriptPath string `env:"OPENBRAIN_VIZ_SCRIPT_PATH"` // path to build-brain-viz.py; empty disables the rebuild endpoint
	VizOutputPath string `env:"OPENBRAIN_VIZ_OUTPUT_PATH"` // path where brain.json is written and served from disk
	// VizPythonPath is the Python interpreter used to run VizScriptPath.
	// Defaults to "python3" resolved off the service's own PATH, which has
	// none of the viz pipeline's dependencies (numpy, umap, hdbscan, psycopg).
	// Point this at a dedicated venv's python binary once one is provisioned.
	VizPythonPath string `env:"OPENBRAIN_VIZ_PYTHON" envDefault:"python3"`
	// VizTTL is the max age of brain.json before /api/rebuild-viz/status
	// reports it stale. Parsed by hand in Load (env:"-" here), not through
	// the struct tag: see parseVizTTL for why an explicitly empty env var
	// must be distinguishable from unset.
	VizTTL time.Duration `env:"-"`
	// VizRebuildTimeout is the safety-bound ceiling on a single
	// build-brain-viz.py run. Not env-configurable (env:"-", no struct tag
	// wired to Load): production always resolves through
	// VizRebuildTimeoutOrDefault(), which falls back to
	// defaultVizRebuildTimeout. This field exists so tests can override it to
	// a few milliseconds and exercise the timeout-classification path without
	// a real 5-minute wait.
	VizRebuildTimeout time.Duration `env:"-"`
}

// DBUrl returns the PostgreSQL connection string.
// When DBHost starts with '/', it is a unix socket directory and the URL uses
// the pgx host= query parameter to avoid OrbStack's localhost TCP intercept.
func (c *Config) DBUrl() string {
	if strings.HasPrefix(c.DBHost, "/") {
		return fmt.Sprintf(
			"postgres://%s:%s@/%s?host=%s&sslmode=disable",
			c.DBUser, c.DBPassword, c.DBName, c.DBHost,
		)
	}
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=disable",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName,
	)
}

// WebAddr returns the host:port for the web server.
func (c *Config) WebAddr() string {
	return fmt.Sprintf("%s:%d", c.WebHost, c.WebPort)
}

// VizPythonInterpreter returns the Python interpreter to run VizScriptPath
// with. Falls back to "python3" when VizPythonPath is empty, so a Config
// built directly (bypassing Load's envDefault, as several tests do) still
// gets the same zero-behavior-change default as an unset env var.
func (c *Config) VizPythonInterpreter() string {
	if c.VizPythonPath == "" {
		return "python3"
	}
	return c.VizPythonPath
}

// defaultVizRebuildTimeout is the safety-bound ceiling on Ollama being slow
// or unreachable, not the expected duration of a rebuild (see plan.md).
const defaultVizRebuildTimeout = 5 * time.Minute

// VizRebuildTimeoutOrDefault returns VizRebuildTimeout when set, or
// defaultVizRebuildTimeout (5 minutes) otherwise. A Config built directly
// with the field left zero (as every production path and most tests do)
// gets the same 5-minute ceiling that predates this field's existence.
func (c *Config) VizRebuildTimeoutOrDefault() time.Duration {
	if c.VizRebuildTimeout <= 0 {
		return defaultVizRebuildTimeout
	}
	return c.VizRebuildTimeout
}

// MCPAllowedHostsList splits MCPAllowedHosts into a trimmed slice, dropping
// empty entries. Loopback hosts are not included here: mcphttp.AllowedHosts
// always permits them regardless of this list's contents.
func (c *Config) MCPAllowedHostsList() []string {
	if c.MCPAllowedHosts == "" {
		return nil
	}
	parts := strings.Split(c.MCPAllowedHosts, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// validateMarkitdownPath checks that the configured binary path is safe:
// either a plain basename (e.g. "markitdown") or an absolute path. Rejects
// path traversal (..), whitespace, and shell metacharacters.
func validateMarkitdownPath(p string) error {
	if p == "" {
		return nil
	}
	if strings.Contains(p, "..") {
		return fmt.Errorf("invalid OPENBRAIN_MARKITDOWN_PATH %q: must not contain path traversal (..)", p)
	}
	if !markitdownPathPattern.MatchString(p) {
		return fmt.Errorf("invalid OPENBRAIN_MARKITDOWN_PATH %q: must be a plain basename or absolute path with no whitespace or shell metacharacters", p)
	}
	return nil
}

// minAuthTokenLen is the minimum acceptable length for auth tokens
// (MCP HTTP, WebSocket, etc.).
const minAuthTokenLen = 32

// minMCPAuthTokenLen is kept as an alias for backward compatibility.
const minMCPAuthTokenLen = minAuthTokenLen

// validateMCPHTTP enforces the conditional auth posture for the MCP HTTP
// transport. When MCP HTTP is enabled and a token is set, the token must be
// sufficiently strong and an OAuth issuer URL must be present (the OAuth 2.0
// metadata endpoints mount only in that case). When MCP HTTP is enabled with
// no token, the transport runs open: this is a deliberate, operator-selected
// posture that mirrors the web surface, and the server logs a loud startup
// warning. Returns an error if validation fails.
func validateMCPHTTP(c *Config) error {
	if !c.MCPHTTPEnabled {
		return nil
	}
	if c.MCPAuthToken == "" {
		// Open mode: no token means no OAuth machinery to validate. The
		// server warns loudly at startup; loopback default bind is the
		// backstop.
		return nil
	}
	if len(c.MCPAuthToken) < minMCPAuthTokenLen {
		return fmt.Errorf("OPENBRAIN_MCP_AUTH_TOKEN must be at least %d characters when OPENBRAIN_MCP_HTTP_ENABLED=true (got %d)", minMCPAuthTokenLen, len(c.MCPAuthToken))
	}
	if c.OAuthIssuer == "" {
		return fmt.Errorf("OPENBRAIN_OAUTH_ISSUER is required when OPENBRAIN_MCP_HTTP_ENABLED=true (e.g. https://openbrain.example.com)")
	}
	if !strings.HasPrefix(c.OAuthIssuer, "https://") && !strings.HasPrefix(c.OAuthIssuer, "http://") {
		return fmt.Errorf("OPENBRAIN_OAUTH_ISSUER must be a full URL with http:// or https:// scheme (got %q)", c.OAuthIssuer)
	}
	return nil
}

// validateWebWSToken checks that when a WebSocket auth token is provided,
// it meets the minimum length requirement. An empty token is allowed
// (WebSocket runs without authentication).
func validateWebWSToken(c *Config) error {
	if c.WebWSToken == "" {
		return nil
	}
	if len(c.WebWSToken) < minAuthTokenLen {
		return fmt.Errorf("OPENBRAIN_WEB_WS_TOKEN must be at least %d characters (got %d)", minAuthTokenLen, len(c.WebWSToken))
	}
	return nil
}

// minOAuthSecretLen is the minimum acceptable length for the OAuth client secret.
const minOAuthSecretLen = 32

// validateOAuth enforces that when OAuth credentials are partially configured,
// both client_id and client_secret are present and the secret is long enough.
func validateOAuth(c *Config) error {
	if !c.MCPHTTPEnabled {
		return nil
	}
	if c.OAuthClientID == "" && c.OAuthClientSecret == "" {
		return nil
	}
	if c.OAuthClientID == "" {
		return fmt.Errorf("OPENBRAIN_OAUTH_CLIENT_ID is required when OPENBRAIN_OAUTH_CLIENT_SECRET is set")
	}
	if c.OAuthClientSecret == "" {
		return fmt.Errorf("OPENBRAIN_OAUTH_CLIENT_SECRET is required when OPENBRAIN_OAUTH_CLIENT_ID is set")
	}
	if len(c.OAuthClientSecret) < minOAuthSecretLen {
		return fmt.Errorf("OPENBRAIN_OAUTH_CLIENT_SECRET must be at least %d characters (got %d)", minOAuthSecretLen, len(c.OAuthClientSecret))
	}
	return nil
}

// defaultVizTTL is the staleness window for brain.json when OPENBRAIN_VIZ_TTL
// is not set at all. See parseVizTTL for the unset-vs-empty distinction.
const defaultVizTTL = 24 * time.Hour

// parseVizTTL resolves OPENBRAIN_VIZ_TTL into a time.Duration.
//
// This is parsed by hand rather than through caarlos0/env's envDefault
// struct tag because that library's default-value resolution (see getOr in
// env.go) collapses an explicitly-empty env var into envDefault whenever a
// default is configured, making OPENBRAIN_VIZ_TTL="" indistinguishable from
// OPENBRAIN_VIZ_TTL unset. The two must differ here: unset means the 24h
// default; "" (or "0"/"0s", any zero duration) means staleness is disabled.
// isSet mirrors the second return value of os.LookupEnv.
func parseVizTTL(raw string, isSet bool) (time.Duration, error) {
	if !isSet {
		return defaultVizTTL, nil
	}
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid OPENBRAIN_VIZ_TTL %q: %w", raw, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("invalid OPENBRAIN_VIZ_TTL %q: must not be negative", raw)
	}
	return d, nil
}

// Load reads .env and parses environment variables into a Config.
// Each call creates a fresh Config — the caller owns the result.
func Load() (*Config, error) {
	_ = godotenv.Load() // ignore error if .env not found
	c := &Config{}
	if err := env.Parse(c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Apply the canonical version default when the env var is not set.
	// version.Version holds "dev" in local builds and the semver string
	// (e.g. "0.3.0") in release builds after @semantic-release/exec rewrites
	// internal/version/version.go. The env var OPENBRAIN_MCP_SERVER_VERSION
	// takes precedence when set, allowing external pinning without code changes.
	if c.MCPServerVersion == "" {
		c.MCPServerVersion = version.Version
	}
	if c.TesseractLangs != "" && !tesseractLangsPattern.MatchString(c.TesseractLangs) {
		return nil, fmt.Errorf("invalid OPENBRAIN_TESSERACT_LANGS %q: must match pattern lang(+lang)* where lang is 3 lowercase letters", c.TesseractLangs)
	}
	if err := validateMarkitdownPath(c.MarkitdownPath); err != nil {
		return nil, err
	}
	if err := validateMCPHTTP(c); err != nil {
		return nil, err
	}
	if err := validateOAuth(c); err != nil {
		return nil, err
	}
	if err := validateWebWSToken(c); err != nil {
		return nil, err
	}
	rawTTL, ttlSet := os.LookupEnv("OPENBRAIN_VIZ_TTL")
	ttl, err := parseVizTTL(rawTTL, ttlSet)
	if err != nil {
		return nil, err
	}
	c.VizTTL = ttl
	return c, nil
}

// MustLoad calls Load and panics on error. Use only in main().
func MustLoad() *Config {
	c, err := Load()
	if err != nil {
		panic(err)
	}
	return c
}

// --- Global convenience for backward compat (will be phased out) ---

var (
	globalCfg atomic.Pointer[Config]
	loadOnce  sync.Once
)

// Get returns the global cached config, loading on first call.
// Prefer Load() + dependency injection for new code.
func Get() *Config {
	loadOnce.Do(func() {
		globalCfg.Store(MustLoad())
	})
	return globalCfg.Load()
}

// Reload re-reads .env and replaces the global config.
func Reload() *Config {
	c := MustLoad()
	globalCfg.Store(c)
	// Reset loadOnce so Get() returns the new config
	loadOnce = sync.Once{}
	loadOnce.Do(func() {}) // mark as done
	slog.Info("config reloaded")
	return c
}
