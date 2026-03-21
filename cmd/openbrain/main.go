// Command openbrain provides CLI access to the OpenBrain knowledge base.
// Subcommands: capture, search, review, stats, import.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/craig8/openbrain/internal/brain"
	"github.com/craig8/openbrain/internal/config"
	"github.com/craig8/openbrain/internal/db"
	"github.com/craig8/openbrain/internal/embeddings"
	"github.com/craig8/openbrain/internal/intent"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cfg := config.MustLoad()
	ctx := context.Background()

	pool, err := db.NewPool(ctx, cfg.DBUrl())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: db connection failed: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	embedder := embeddings.NewOllamaEmbedder(cfg)
	b := brain.New(pool, embedder, cfg)

	switch os.Args[1] {
	case "capture":
		err = cmdCapture(ctx, b)
	case "search":
		err = cmdSearch(ctx, b)
	case "review":
		err = cmdReview(ctx, b)
	case "stats":
		err = cmdStats(ctx, b)
	case "import":
		err = fmt.Errorf("import not yet implemented — use MCP bulk_import tool")
	default:
		msg := strings.Join(os.Args[1:], " ")
		parsed := intent.Parse(msg)
		var result string
		result, err = b.Dispatch(ctx, parsed, "cli")
		if err == nil {
			fmt.Println(result)
		}
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdCapture(ctx context.Context, b *brain.Brain) error {
	if len(os.Args) < 3 {
		return fmt.Errorf("usage: openbrain capture <text>")
	}
	text := strings.Join(os.Args[2:], " ")
	parsed := intent.ParsedIntent{
		Intent:      intent.Capture,
		Text:        text,
		ThoughtType: intent.InferType(text),
	}
	result, err := b.Dispatch(ctx, parsed, "cli")
	if err != nil {
		return err
	}
	fmt.Println(result)
	return nil
}

func cmdSearch(ctx context.Context, b *brain.Brain) error {
	if len(os.Args) < 3 {
		return fmt.Errorf("usage: openbrain search <query>")
	}
	query := strings.Join(os.Args[2:], " ")
	parsed := intent.ParsedIntent{Intent: intent.Search, Text: query, ThoughtType: "note"}
	result, err := b.Dispatch(ctx, parsed, "cli")
	if err != nil {
		return err
	}
	fmt.Println(result)
	return nil
}

func cmdReview(ctx context.Context, b *brain.Brain) error {
	parsed := intent.ParsedIntent{Intent: intent.Review, Text: "review", ThoughtType: "note"}
	result, err := b.Dispatch(ctx, parsed, "cli")
	if err != nil {
		return err
	}
	fmt.Println(result)
	return nil
}

func cmdStats(ctx context.Context, b *brain.Brain) error {
	parsed := intent.ParsedIntent{Intent: intent.Stats, Text: "stats", ThoughtType: "note"}
	result, err := b.Dispatch(ctx, parsed, "cli")
	if err != nil {
		return err
	}
	fmt.Println(result)
	return nil
}

func printUsage() {
	fmt.Println(`OpenBrain CLI — personal knowledge base

Usage:
  openbrain capture <text>     Capture a thought
  openbrain search <query>     Search for thoughts
  openbrain review             Weekly review
  openbrain stats              Show statistics
  openbrain import <file>      Import from JSON file
  openbrain <text>             Auto-classify and dispatch`)
}
