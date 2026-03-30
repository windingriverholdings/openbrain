// Command openbrain-slack runs the Slack bot in socket mode.
package main

import (
	"log/slog"
	"os"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	// TODO: implement Slack bot
	slog.Info("slack bot not yet implemented")
}
