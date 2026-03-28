// Package config loads application settings from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

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

	// Retrieval
	SearchTopK           int     `env:"OPENBRAIN_SEARCH_TOP_K" envDefault:"10"`
	SearchScoreThreshold float64 `env:"OPENBRAIN_SEARCH_SCORE_THRESHOLD" envDefault:"0.15"`

	// Telegram
	TelegramBotToken      string `env:"OPENBRAIN_TELEGRAM_BOT_TOKEN"`
	TelegramAllowedUserID int64  `env:"OPENBRAIN_TELEGRAM_ALLOWED_USER_ID"`

	// Slack
	SlackBotToken      string `env:"OPENBRAIN_SLACK_BOT_TOKEN"`
	SlackAppToken      string `env:"OPENBRAIN_SLACK_APP_TOKEN"`
	SlackAllowedUserID string `env:"OPENBRAIN_SLACK_ALLOWED_USER_ID"`

	// Web UI
	WebHost string `env:"OPENBRAIN_WEB_HOST" envDefault:"127.0.0.1"`
	WebPort int    `env:"OPENBRAIN_WEB_PORT" envDefault:"10203"`

	// LLM extraction
	ExtractProvider      string `env:"OPENBRAIN_EXTRACT_PROVIDER" envDefault:"none"`
	ExtractModel         string `env:"OPENBRAIN_EXTRACT_MODEL" envDefault:"gemma3"`
	ExtractModelFast     string `env:"OPENBRAIN_EXTRACT_MODEL_FAST"`
	ExtractFastThreshold int    `env:"OPENBRAIN_EXTRACT_FAST_THRESHOLD" envDefault:"500"`
	OllamaBaseURL        string `env:"OPENBRAIN_OLLAMA_BASE_URL" envDefault:"http://localhost:11434"`
	AnthropicAPIKey      string `env:"OPENBRAIN_ANTHROPIC_API_KEY"`

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

// Load reads .env and parses environment variables into a Config.
// Each call creates a fresh Config — the caller owns the result.
func Load() (*Config, error) {
	_ = godotenv.Load() // ignore error if .env not found
	c := &Config{}
	if err := env.Parse(c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
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
