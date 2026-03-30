// Command openbrain-telegram runs the Telegram bot in polling mode.
package main

import (
	"log/slog"
	"os"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	// TODO: implement Telegram bot
	slog.Info("telegram bot not yet implemented")
}
