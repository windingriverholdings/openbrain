// Package config loads application settings from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
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
	DBPassword string `env:"OPENBRAIN_DB_PASSWORD" envDefault:"openbrain"`

	// Embedding
	EmbeddingModel string `env:"OPENBRAIN_EMBEDDING_MODEL" envDefault:"all-minilm"`
	EmbeddingDim   int    `env:"OPENBRAIN_EMBEDDING_DIM" envDefault:"384"`

	// MCP
	MCPServerName    string `env:"OPENBRAIN_MCP_SERVER_NAME" envDefault:"openbrain"`
	MCPServerVersion string `env:"OPENBRAIN_MCP_SERVER_VERSION" envDefault:"0.1.0"`
	MCPHTTPEnabled   bool   `env:"OPENBRAIN_MCP_HTTP_ENABLED" envDefault:"false"`
	MCPAuthToken     string `env:"OPENBRAIN_MCP_AUTH_TOKEN"`

	// OAuth (for Claude.ai MCP connector)
	OAuthClientID     string `env:"OPENBRAIN_OAUTH_CLIENT_ID"`
	OAuthClientSecret string `env:"OPENBRAIN_OAUTH_CLIENT_SECRET"`
	OAuthIssuer       string `env:"OPENBRAIN_OAUTH_ISSUER" envDefault:"https://openbrain.wr-s.net"`

	// Retrieval
	SearchTopK              int     `env:"OPENBRAIN_SEARCH_TOP_K" envDefault:"10"`
	SearchScoreThreshold    float64 `env:"OPENBRAIN_SEARCH_SCORE_THRESHOLD" envDefault:"0.15"`
	SearchFilteredThreshold float64 `env:"OPENBRAIN_SEARCH_FILTERED_THRESHOLD" envDefault:"0.01"`

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

	// Document ingestion
	IngestDir          string `env:"OPENBRAIN_INGEST_DIR"`
	IngestMaxBytes     int64  `env:"OPENBRAIN_INGEST_MAX_BYTES" envDefault:"52428800"` // 50 MB
	IngestChunkSize    int    `env:"OPENBRAIN_INGEST_CHUNK_SIZE" envDefault:"2000"`
	IngestChunkOverlap int    `env:"OPENBRAIN_INGEST_CHUNK_OVERLAP" envDefault:"200"`
	TesseractLangs     string `env:"OPENBRAIN_TESSERACT_LANGS" envDefault:"eng"`

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
}

// DBUrl returns the PostgreSQL connection string.
func (c *Config) DBUrl() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=disable",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName,
	)
}

// WebAddr returns the host:port for the web server.
func (c *Config) WebAddr() string {
	return fmt.Sprintf("%s:%d", c.WebHost, c.WebPort)
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

// minMCPAuthTokenLen is the minimum acceptable length for the MCP auth token.
const minMCPAuthTokenLen = 32

// validateMCPHTTP enforces that when MCP HTTP is enabled, a sufficiently
// strong auth token is configured. Returns an error if validation fails.
func validateMCPHTTP(c *Config) error {
	if !c.MCPHTTPEnabled {
		return nil
	}
	if c.MCPAuthToken == "" {
		return fmt.Errorf("OPENBRAIN_MCP_AUTH_TOKEN is required when OPENBRAIN_MCP_HTTP_ENABLED=true")
	}
	if len(c.MCPAuthToken) < minMCPAuthTokenLen {
		return fmt.Errorf("OPENBRAIN_MCP_AUTH_TOKEN must be at least %d characters when OPENBRAIN_MCP_HTTP_ENABLED=true (got %d)", minMCPAuthTokenLen, len(c.MCPAuthToken))
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

// Load reads .env and parses environment variables into a Config.
// Each call creates a fresh Config — the caller owns the result.
func Load() (*Config, error) {
	_ = godotenv.Load() // ignore error if .env not found
	c := &Config{}
	if err := env.Parse(c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
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
