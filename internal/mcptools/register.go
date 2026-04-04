// Package mcptools registers OpenBrain MCP tools on a server.MCPServer.
// This is shared by both the stdio transport (cmd/openbrain-mcp) and
// the HTTP transport (mounted on cmd/openbrain-web).
package mcptools

import (
	"context"

	"github.com/craig8/openbrain/internal/brain"
	"github.com/craig8/openbrain/internal/config"
	"github.com/craig8/openbrain/internal/embeddings"
	"github.com/craig8/openbrain/internal/extract"
	"github.com/craig8/openbrain/internal/intent"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var thoughtTypeEnum = []string{"decision", "insight", "person", "meeting", "idea", "note", "memory"}

var validThoughtTypes = map[string]bool{
	"decision": true, "insight": true, "person": true,
	"meeting": true, "idea": true, "note": true, "memory": true,
}

var validSearchModes = map[string]bool{
	"hybrid": true, "vector": true, "keyword": true,
}

// sourceMaxLen is the maximum allowed length for the source parameter.
const sourceMaxLen = 255

// extractFunc is a seam for testing extraction without LLM.
var extractFunc = func(ctx context.Context, text string) ([]extract.Candidate, error) {
	return extract.ExtractThoughts(ctx, text)
}

// RegisterOpts controls which tools are registered on the MCP server.
type RegisterOpts struct {
	// ExcludeIngest skips the ingest_document tool. This MUST be true
	// when the MCP server is exposed over HTTP to prevent remote filesystem reads.
	ExcludeIngest bool
}

// RegisterTools adds all OpenBrain tools to the given MCP server.
// Both b and embedder may be nil for testing tool registration only.
func RegisterTools(s *server.MCPServer, b *brain.Brain, embedder embeddings.Embedder) {
	RegisterToolsWithOpts(s, b, embedder, RegisterOpts{})
}

// RegisterToolsWithOpts adds OpenBrain tools to the given MCP server,
// respecting the provided options to exclude certain tools.
func RegisterToolsWithOpts(s *server.MCPServer, b *brain.Brain, embedder embeddings.Embedder, opts RegisterOpts) {
	cfg := config.Get()

	if !opts.ExcludeIngest {
		s.AddTool(
			mcp.NewTool("ingest_document",
				mcp.WithDescription("Ingest a document (PDF, DOCX, or image via OCR) into OpenBrain. Extracts text and optionally auto-captures as thoughts."),
				mcp.WithString("file_path", mcp.Required(), mcp.Description("Absolute path to the document file")),
				mcp.WithString("source", mcp.Description("Source identifier for captured thoughts")),
				mcp.WithBoolean("auto_capture", mcp.Description("Auto-capture extracted text as thoughts (default: true)")),
			),
			mcpIngestDocument(b, cfg),
		)
	}

	s.AddTool(
		mcp.NewTool("capture_thought",
			mcp.WithDescription("Capture a thought into OpenBrain."),
			mcp.WithString("content", mcp.Required(), mcp.Description("The thought content")),
			mcp.WithString("thought_type", mcp.Enum(thoughtTypeEnum...), mcp.Description("Type of thought")),
			mcp.WithArray("tags", mcp.Description("Tags for the thought")),
			mcp.WithString("summary", mcp.Description("Optional short summary")),
			mcp.WithString("source", mcp.Description("Source identifier")),
		),
		mcpCapture(b),
	)

	s.AddTool(
		mcp.NewTool("search_thoughts",
			mcp.WithDescription("Search OpenBrain for thoughts related to a query."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Natural language search query")),
			mcp.WithNumber("top_k", mcp.Description("Maximum number of results to return")),
			mcp.WithString("mode", mcp.Enum("hybrid", "vector", "keyword"), mcp.Description("Search mode")),
			mcp.WithString("thought_type", mcp.Enum(thoughtTypeEnum...), mcp.Description("Filter by thought type")),
			mcp.WithArray("tags", mcp.Description("Filter to thoughts with any of these tags")),
			mcp.WithBoolean("include_history", mcp.Description("Include superseded thoughts")),
		),
		mcpSearch(b),
	)

	s.AddTool(
		mcp.NewTool("weekly_review",
			mcp.WithDescription("Get a review of thoughts from the past N days."),
			mcp.WithNumber("days", mcp.Description("Number of days to review")),
		),
		mcpDispatch(b, intent.Review),
	)

	s.AddTool(
		mcp.NewTool("brain_stats",
			mcp.WithDescription("Return aggregate statistics about the OpenBrain knowledge base."),
		),
		mcpDispatch(b, intent.Stats),
	)

	s.AddTool(
		mcp.NewTool("bulk_import",
			mcp.WithDescription("Import multiple thoughts at once."),
			mcp.WithArray("thoughts", mcp.Required(), mcp.Description("Array of thought objects to import")),
			mcp.WithString("source", mcp.Description("Source identifier")),
		),
		mcpBulkImport(b),
	)

	s.AddTool(
		mcp.NewTool("thought_timeline",
			mcp.WithDescription("Get the timeline of thoughts about a subject."),
			mcp.WithString("subject", mcp.Required(), mcp.Description("Subject name to get timeline for")),
			mcp.WithNumber("top_k", mcp.Description("Maximum number of results")),
		),
		mcpDispatch(b, intent.Search),
	)

	s.AddTool(
		mcp.NewTool("extract_thoughts",
			mcp.WithDescription("Extract structured thoughts from long-form text using LLM."),
			mcp.WithString("text", mcp.Required(), mcp.Description("Long-form text to extract from")),
			mcp.WithBoolean("auto_capture", mcp.Description("Auto-capture extracted thoughts")),
			mcp.WithString("source", mcp.Description("Source identifier")),
		),
		mcpExtract(b),
	)

	s.AddTool(
		mcp.NewTool("supersede_thought",
			mcp.WithDescription("Capture a new thought and mark an older thought as superseded. Use when updated knowledge replaces a previous belief, decision, or fact. Provide old_thought_id to supersede directly, or let OpenBrain find the best match via supersedes_query."),
			mcp.WithString("content", mcp.Required(), mcp.Description("The new thought that replaces the old one")),
			mcp.WithString("supersedes_query", mcp.Description("Search query to find the thought being superseded; defaults to the new content")),
			mcp.WithString("old_thought_id", mcp.Description("UUID of the thought to supersede directly (skips search)")),
			mcp.WithString("thought_type", mcp.Enum(thoughtTypeEnum...), mcp.Description("Type of the new thought")),
			mcp.WithArray("tags", mcp.Description("Tags for the new thought")),
			mcp.WithString("source", mcp.Description("Source identifier")),
			mcp.WithString("summary", mcp.Description("Optional short summary")),
		),
		mcpSupersede(b),
	)
}
