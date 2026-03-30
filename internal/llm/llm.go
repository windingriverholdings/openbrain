// Package llm provides an abstraction over LLM providers (Ollama, Claude API)
// with smart routing between fast and primary models.
package llm

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"

	"github.com/craig8/openbrain/internal/config"
)

// Provider generates text from a prompt.
type Provider interface {
	Generate(ctx context.Context, prompt, system string) (string, error)
}

var correctionPatterns = regexp.MustCompile(
	`(?i)(actually[,:\s]|correction[,:\s]|update[,:\s]|no longer[,:\s]` +
		`|now instead[,:\s]|changed?[,:\s]|not \d+%.*but \d+%` +
		`|was wrong|I was mistaken|turns out)`)

// NeedsPrimaryModel returns true if text should use the primary (accurate) model.
func NeedsPrimaryModel(text string, threshold int) bool {
	if len(text) > threshold {
		return true
	}
	return correctionPatterns.MatchString(text)
}

var (
	primary *Provider
	fast    *Provider
	hasFast bool
	mu      sync.Mutex
)

// ResetProviders clears cached providers so the next GetProvider call rebuilds from config.
func ResetProviders() {
	mu.Lock()
	defer mu.Unlock()
	primary = nil
	fast = nil
	hasFast = false
	slog.Info("llm providers reset")
}

// GetProvider returns the appropriate LLM provider for the given text.
// Short/simple text gets the fast provider; long or correction-heavy text gets primary.
func GetProvider(ctx context.Context, text string) (Provider, error) {
	mu.Lock()
	defer mu.Unlock()

	cfg := config.Get()

	if cfg.ExtractProvider == "none" {
		return nil, nil
	}

	if primary == nil {
		p, err := buildProvider(cfg.ExtractProvider, cfg.ExtractModel, cfg)
		if err != nil {
			return nil, err
		}
		primary = &p

		hasFast = cfg.ExtractModelFast != "" && cfg.ExtractModelFast != cfg.ExtractModel
		if hasFast {
			f, err := buildProvider(cfg.ExtractProvider, cfg.ExtractModelFast, cfg)
			if err != nil {
				return nil, err
			}
			fast = &f
			slog.Info("llm providers ready",
				"provider", cfg.ExtractProvider,
				"primary", cfg.ExtractModel,
				"fast", cfg.ExtractModelFast)
		} else {
			slog.Info("llm provider ready",
				"provider", cfg.ExtractProvider,
				"model", cfg.ExtractModel)
		}
	}

	if hasFast && text != "" && !NeedsPrimaryModel(text, cfg.ExtractFastThreshold) {
		return *fast, nil
	}

	return *primary, nil
}

func buildProvider(providerType, model string, cfg *config.Config) (Provider, error) {
	switch providerType {
	case "ollama":
		return NewOllamaProvider(cfg.OllamaBaseURL, model), nil
	case "claude":
		if cfg.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("OPENBRAIN_ANTHROPIC_API_KEY required for claude provider")
		}
		return NewClaudeProvider(cfg.AnthropicAPIKey, model), nil
	default:
		return nil, fmt.Errorf("unknown extract_provider: %s", providerType)
	}
}
