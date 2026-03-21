// Command openbrain-watchd runs the sandbox file bridge daemon.
package main

import (
	"log/slog"
	"os"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	// TODO: implement watchd daemon
	slog.Info("watchd daemon not yet implemented")
}
