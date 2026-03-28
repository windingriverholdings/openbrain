package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/craig8/openbrain/internal/brain"
	"github.com/craig8/openbrain/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// mcpIngestDocument handles the ingest_document MCP tool.
// SECURITY: validates paths, never returns raw file content.
func mcpIngestDocument(b *brain.Brain, cfg *config.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		filePath, _ := args["file_path"].(string)
		source := stringArg(args, "source", "claude")
		autoCapture := boolArg(args, "auto_capture", true)

		if err := validateMCPIngestPath(filePath, cfg.IngestDir); err != nil {
			return toolError(sanitizeIngestError(err.Error())), nil
		}

		result, err := b.IngestDocument(ctx, filePath, source, autoCapture)
		if err != nil {
			return toolError(sanitizeIngestError(err.Error())), nil
		}

		return toolText(result), nil
	}
}

// validateMCPIngestPath validates an ingestion file path at the MCP layer.
// Rejects empty, relative, traversal, and out-of-bounds paths including symlinks.
func validateMCPIngestPath(path, allowedDir string) error {
	if path == "" {
		return fmt.Errorf("file_path is required")
	}

	if allowedDir == "" {
		return fmt.Errorf("ingestion not configured: OPENBRAIN_INGEST_DIR not set")
	}

	if !filepath.IsAbs(path) {
		return fmt.Errorf("file_path must be an absolute path")
	}

	// Clean and check for .. components
	cleaned := filepath.Clean(path)
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == ".." {
			return fmt.Errorf("path outside allowed ingestion directory")
		}
	}

	// Resolve allowed dir
	allowedResolved, err := filepath.EvalSymlinks(filepath.Clean(allowedDir))
	if err != nil {
		return fmt.Errorf("ingestion directory unavailable")
	}

	// Prefix check
	if !strings.HasPrefix(cleaned, allowedResolved+string(filepath.Separator)) && cleaned != allowedResolved {
		return fmt.Errorf("path outside allowed ingestion directory")
	}

	// Resolve symlinks for the actual file
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		// If file doesn't exist, resolve parent
		resolved, err = filepath.EvalSymlinks(filepath.Dir(cleaned))
		if err != nil {
			return fmt.Errorf("path not accessible")
		}
		resolved = filepath.Join(resolved, filepath.Base(cleaned))
	}

	// Final resolved check
	if !strings.HasPrefix(resolved, allowedResolved+string(filepath.Separator)) && resolved != allowedResolved {
		return fmt.Errorf("path outside allowed ingestion directory")
	}

	return nil
}

// sanitizeIngestError removes internal path information from error messages
// to prevent information leakage through the MCP interface.
func sanitizeIngestError(errMsg string) string {
	// Replace patterns that look like file paths with generic messages
	if strings.Contains(errMsg, "no such file") {
		return "file not found"
	}
	if strings.Contains(errMsg, "permission denied") {
		return "file not accessible"
	}
	if strings.Contains(errMsg, "outside allowed") {
		return "file path not allowed"
	}
	if strings.Contains(errMsg, "not configured") {
		return "document ingestion is not configured"
	}
	if strings.Contains(errMsg, "unsupported") {
		return errMsg // format errors are safe to return
	}

	// For any other error, return a generic message
	return "ingestion failed"
}
