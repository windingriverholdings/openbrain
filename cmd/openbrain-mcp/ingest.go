package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/craig8/openbrain/internal/brain"
	"github.com/craig8/openbrain/internal/config"
	"github.com/craig8/openbrain/internal/pathsec"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// sourceMaxLen is the maximum allowed length for the source parameter.
const sourceMaxLen = 255

// mcpIngestDocument handles the ingest_document MCP tool.
// SECURITY: validates paths, never returns raw file content.
func mcpIngestDocument(b *brain.Brain, cfg *config.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		filePath, _ := args["file_path"].(string)
		source := stringArg(args, "source", "claude")
		autoCapture := boolArg(args, "auto_capture", true)

		if len([]rune(source)) > sourceMaxLen {
			return toolError("source parameter exceeds 255 character limit"), nil
		}

		if err := pathsec.ValidateIngestPath(filePath, cfg.IngestDir); err != nil {
			return toolError(sanitizeIngestError(err.Error())), nil
		}

		result, err := b.IngestDocument(ctx, filePath, source, autoCapture)
		if err != nil {
			return toolError(sanitizeIngestError(err.Error())), nil
		}

		return toolText(result), nil
	}
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
	if strings.Contains(errMsg, "file too large") {
		return fmt.Sprintf("file too large (limit: configurable via OPENBRAIN_INGEST_MAX_BYTES)")
	}

	// For any other error, return a generic message
	return "ingestion failed"
}
