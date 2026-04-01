package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/craig8/openbrain/internal/brain"
	"github.com/craig8/openbrain/internal/config"
	"github.com/craig8/openbrain/internal/embeddings"
	"github.com/craig8/openbrain/internal/extract"
	"github.com/craig8/openbrain/internal/intent"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func serveMCP(_ context.Context, cfg *config.Config, b *brain.Brain, embedder embeddings.Embedder) error {
	s := server.NewMCPServer(cfg.MCPServerName, cfg.MCPServerVersion)
	registerTools(s, b, embedder)
	return server.ServeStdio(s)
}

var thoughtTypeEnum = []string{"decision", "insight", "person", "meeting", "idea", "note", "memory"}

var validThoughtTypes = map[string]bool{
	"decision": true, "insight": true, "person": true,
	"meeting": true, "idea": true, "note": true, "memory": true,
}

var validSearchModes = map[string]bool{
	"hybrid": true, "vector": true, "keyword": true,
}

func registerTools(s *server.MCPServer, b *brain.Brain, embedder embeddings.Embedder) {
	cfg := config.Get()

	s.AddTool(
		mcp.NewTool("ingest_document",
			mcp.WithDescription("Ingest a document (PDF, DOCX, or image via OCR) into OpenBrain. Extracts text and optionally auto-captures as thoughts."),
			mcp.WithString("file_path", mcp.Required(), mcp.Description("Absolute path to the document file")),
			mcp.WithString("source", mcp.Description("Source identifier for captured thoughts")),
			mcp.WithBoolean("auto_capture", mcp.Description("Auto-capture extracted text as thoughts (default: true)")),
		),
		mcpIngestDocument(b, cfg),
	)

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
		mcpDispatch(b, intent.Search), // delegates to brain
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

// mcpCapture routes capture through brain.Capture.
func mcpCapture(b *brain.Brain) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		content, _ := args["content"].(string)
		thoughtType, _ := args["thought_type"].(string)
		if thoughtType == "" {
			thoughtType = intent.InferType(content)
		}
		source := stringArg(args, "source", "claude")
		tags := stringListArg(args, "tags")

		parsed := intent.ParsedIntent{
			Intent:      intent.Capture,
			Text:        content,
			ThoughtType: thoughtType,
			Tags:        tags,
		}
		result, err := b.Capture(ctx, parsed, source)
		if err != nil {
			return toolError(err.Error()), nil
		}
		return toolText(result), nil
	}
}

// mcpSearch routes search through brain.Search with formatted output.
func mcpSearch(b *brain.Brain) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		query, _ := args["query"].(string)

		mode := stringArg(args, "mode", "hybrid")
		if !validSearchModes[mode] {
			return toolError(fmt.Sprintf("invalid mode %q: must be one of hybrid, vector, keyword", mode)), nil
		}

		thoughtType := stringArg(args, "thought_type", "")
		if thoughtType != "" && !validThoughtTypes[thoughtType] {
			return toolError(fmt.Sprintf("invalid thought_type %q: must be one of decision, insight, person, meeting, idea, note, memory", thoughtType)), nil
		}

		opts := brain.SearchOpts{
			Mode:           mode,
			ThoughtType:    thoughtType,
			Tags:           stringListArg(args, "tags"),
			IncludeHistory: boolArg(args, "include_history", false),
		}

		results, err := b.Search(ctx, query, opts)
		if err != nil {
			return toolError(err.Error()), nil
		}

		if len(results) == 0 {
			return toolText("No matching thoughts found."), nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Found %d thought(s) related to: %q\n\n", len(results), query)
		for i, t := range results {
			score := ""
			if t.Score != nil {
				score = fmt.Sprintf(" (score: %.4f)", *t.Score)
			}
			fmt.Fprintf(&sb, "%d. [%s]%s — %s\n   %s\n",
				i+1, t.ThoughtType, score, t.CreatedAt.Format("2006-01-02"), t.Content)
			if len(t.Tags) > 0 {
				fmt.Fprintf(&sb, "   Tags: %s\n", strings.Join(t.Tags, ", "))
			}
			sb.WriteString("\n")
		}

		return toolText(sb.String()), nil
	}
}

// mcpDispatch routes any intent through brain.Dispatch.
func mcpDispatch(b *brain.Brain, i intent.Intent) server.ToolHandlerFunc {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parsed := intent.ParsedIntent{Intent: i, Text: string(i), ThoughtType: "note"}
		result, err := b.Dispatch(ctx, parsed, "claude")
		if err != nil {
			return toolError(err.Error()), nil
		}
		return toolText(result), nil
	}
}

// mcpBulkImport imports multiple thoughts through brain.Capture.
func mcpBulkImport(b *brain.Brain) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		thoughts, ok := args["thoughts"].([]any)
		if !ok {
			return toolError("thoughts must be an array"), nil
		}
		source := stringArg(args, "source", "import")

		var imported int
		var errs []string
		for _, t := range thoughts {
			obj, ok := t.(map[string]any)
			if !ok {
				continue
			}

			content, _ := obj["content"].(string)
			if content == "" {
				continue
			}

			thoughtType, _ := obj["thought_type"].(string)
			if thoughtType == "" {
				thoughtType = intent.InferType(content)
			}

			parsed := intent.ParsedIntent{
				Intent:      intent.Capture,
				Text:        content,
				ThoughtType: thoughtType,
				Tags:        stringListFromObj(obj, "tags"),
			}

			_, err := b.Capture(ctx, parsed, source)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			imported++
		}

		result := fmt.Sprintf("Imported %d/%d thoughts", imported, len(thoughts))
		if len(errs) > 0 {
			result += fmt.Sprintf("\nErrors: %s", strings.Join(errs, "; "))
		}
		return toolText(result), nil
	}
}

// mcpExtract routes through brain.DeepCapture.
func mcpExtract(b *brain.Brain) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		text, _ := args["text"].(string)
		autoCapture := true
		if ac, ok := args["auto_capture"].(bool); ok {
			autoCapture = ac
		}
		source := stringArg(args, "source", "claude")

		if !autoCapture {
			// Return raw extraction without capturing
			candidates, err := extractOnly(ctx, text)
			if err != nil {
				return toolError(err.Error()), nil
			}
			data, _ := json.MarshalIndent(candidates, "", "  ")
			return toolText(fmt.Sprintf("Extracted %d candidates:\n%s", len(candidates), data)), nil
		}

		parsed := intent.ParsedIntent{Intent: intent.Extract, Text: text, ThoughtType: "note"}
		result, err := b.DeepCapture(ctx, parsed, source)
		if err != nil {
			return toolError(err.Error()), nil
		}
		return toolText(result), nil
	}
}

// mcpSupersede routes through brain.Supersede with optional query/ID overrides.
func mcpSupersede(b *brain.Brain) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		content, _ := args["content"].(string)
		thoughtType := stringArg(args, "thought_type", "")
		if thoughtType == "" {
			thoughtType = intent.InferType(content)
		}
		source := stringArg(args, "source", "claude")
		tags := stringListArg(args, "tags")

		parsed := intent.ParsedIntent{
			Intent:      intent.Supersede,
			Text:        content,
			ThoughtType: thoughtType,
			Tags:        tags,
		}

		if q := stringArg(args, "supersedes_query", ""); q != "" {
			parsed.SupersedeQuery = &q
		}
		if id := stringArg(args, "old_thought_id", ""); id != "" {
			parsed.OldThoughtID = &id
		}

		result, err := b.Supersede(ctx, parsed, source)
		if err != nil {
			return toolError(err.Error()), nil
		}
		return toolText(result), nil
	}
}

// extractOnly calls the extract package without capturing.
func extractOnly(ctx context.Context, text string) ([]any, error) {
	candidates, err := extractPkg(ctx, text)
	if err != nil {
		return nil, err
	}
	// Convert to []any for JSON serialization
	result := make([]any, len(candidates))
	for i, c := range candidates {
		result[i] = c
	}
	return result, nil
}

var extractPkg = func(ctx context.Context, text string) ([]extract.Candidate, error) {
	return extract.ExtractThoughts(ctx, text)
}

// --- Helpers ---

func toolText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(text)},
	}
}

func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(text)},
		IsError: true,
	}
}

func stringArg(args map[string]any, key, fallback string) string {
	if v, ok := args[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

func boolArg(args map[string]any, key string, fallback bool) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return fallback
}

func stringListArg(args map[string]any, key string) []string {
	arr, ok := args[key].([]any)
	if !ok {
		return nil
	}
	var result []string
	for _, v := range arr {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func stringListFromObj(obj map[string]any, key string) []string {
	arr, ok := obj[key].([]any)
	if !ok {
		return nil
	}
	var result []string
	for _, v := range arr {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
