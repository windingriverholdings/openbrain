package main

import (
	"context"

	"github.com/craig8/openbrain/internal/brain"
	"github.com/craig8/openbrain/internal/config"
	"github.com/craig8/openbrain/internal/embeddings"
	"github.com/craig8/openbrain/internal/mcptools"
	"github.com/mark3labs/mcp-go/server"
)

func serveMCP(_ context.Context, cfg *config.Config, b *brain.Brain, embedder embeddings.Embedder) error {
	s := server.NewMCPServer(cfg.MCPServerName, cfg.MCPServerVersion)
	mcptools.RegisterTools(s, b, embedder)
	return server.ServeStdio(s)
}
