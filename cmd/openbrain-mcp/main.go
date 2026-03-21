// Command openbrain-mcp runs the OpenBrain MCP server over stdio,
// exposing tools for Claude Code integration.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/craig8/openbrain/internal/brain"
	"github.com/craig8/openbrain/internal/config"
	"github.com/craig8/openbrain/internal/db"
	"github.com/craig8/openbrain/internal/embeddings"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg := config.MustLoad()
	ctx := context.Background()

	pool, err := db.NewPool(ctx, cfg.DBUrl())
	if err != nil {
		slog.Error("db connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	embedder := embeddings.NewOllamaEmbedder(cfg)
	b := brain.New(pool, embedder, cfg)

	if err := serveMCP(ctx, cfg, b, embedder); err != nil {
		slog.Error("mcp server failed", "error", err)
		os.Exit(1)
	}
}
