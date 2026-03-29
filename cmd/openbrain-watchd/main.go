// Command openbrain-watchd runs the folder watcher daemon that auto-ingests
// documents when files are created or modified in configured watch directories.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/craig8/openbrain/internal/brain"
	"github.com/craig8/openbrain/internal/config"
	"github.com/craig8/openbrain/internal/db"
	"github.com/craig8/openbrain/internal/embeddings"
	"github.com/craig8/openbrain/internal/watcher"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg := config.MustLoad()

	if cfg.WatchDirs == "" {
		slog.Error("OPENBRAIN_WATCH_DIRS not set — nothing to watch")
		os.Exit(1)
	}

	dirs := watcher.ParseWatchDirs(cfg.WatchDirs)
	slog.Info("configured watch directories", "count", len(dirs), "dirs", dirs)

	// Determine state file path
	statePath := cfg.WatchStateFile
	if statePath == "" {
		if cfg.IngestDir != "" {
			statePath = filepath.Join(cfg.IngestDir, ".watchd-state.json")
		} else {
			statePath = filepath.Join(os.TempDir(), "openbrain-watchd-state.json")
		}
	}
	cfg.WatchStateFile = statePath

	// Load persisted state
	state, err := watcher.LoadState(statePath)
	if err != nil {
		slog.Error("failed to load state", "path", statePath, "error", err)
		os.Exit(1)
	}
	slog.Info("loaded watcher state", "path", statePath, "tracked_files", len(state.Files))

	// Connect to database
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := db.NewPool(ctx, cfg.DBUrl())
	if err != nil {
		slog.Error("db connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Create embedder and brain
	embedder := embeddings.NewOllamaEmbedder(cfg)
	b := brain.New(pool, embedder, cfg)
	adapter := watcher.NewBrainAdapter(b)

	// Create watcher
	w, err := watcher.New(adapter, cfg, state)
	if err != nil {
		slog.Error("failed to create watcher", "error", err)
		os.Exit(1)
	}

	// Graceful shutdown on SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	slog.Info("starting watchd daemon")
	if err := w.Watch(ctx); err != nil {
		slog.Error("watcher exited with error", "error", err)
		os.Exit(1)
	}

	// Save state on clean exit
	if err := state.Save(statePath); err != nil {
		slog.Warn("failed to save state on exit", "error", err)
	}
	slog.Info("watchd daemon stopped")
}
