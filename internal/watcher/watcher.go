package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/craig8/openbrain/internal/config"
)

// Ingester is the interface the watcher uses to ingest files. This allows
// testing with a mock instead of a real Brain+DB.
type Ingester interface {
	IngestFile(ctx context.Context, filePath, source string, metadata map[string]any) (string, error)
}

// defaultWorkerPoolSize is the maximum number of concurrent ingestion goroutines.
const defaultWorkerPoolSize = 4

// Watcher monitors directories for file changes and auto-ingests documents.
type Watcher struct {
	ingester    Ingester
	cfg         *config.Config
	state       *State
	debounceMs  int
	fsw         *fsnotify.Watcher
	pending     map[string]*time.Timer
	pendingLock sync.Mutex
	sem         chan struct{} // bounded worker pool semaphore
}

// New creates a Watcher. Does not start watching — call Watch to begin.
func New(ingester Ingester, cfg *config.Config, state *State) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}

	debounce := cfg.WatchDebounceMs
	if debounce <= 0 {
		debounce = 500
	}

	return &Watcher{
		ingester:   ingester,
		cfg:        cfg,
		state:      state,
		debounceMs: debounce,
		fsw:        fsw,
		pending:    make(map[string]*time.Timer),
		sem:        make(chan struct{}, defaultWorkerPoolSize),
	}, nil
}

// Watch starts watching all configured directories. Blocks until ctx is cancelled.
func (w *Watcher) Watch(ctx context.Context) error {
	dirs := ParseWatchDirs(w.cfg.WatchDirs)
	valid, err := validateWatchDirs(dirs, w.cfg.IngestDir)
	if err != nil {
		return fmt.Errorf("watch dir validation: %w", err)
	}

	if len(valid) == 0 {
		return fmt.Errorf("no valid watch directories configured")
	}

	slog.Info("watching top-level files only; subdirectories not monitored")

	// Startup scan: ingest files added while daemon was down
	for _, dir := range valid {
		scanned, scanErr := w.ScanDir(ctx, dir)
		if scanErr != nil {
			slog.Warn("startup scan failed", "dir", dir, "error", scanErr)
		} else {
			slog.Info("startup scan complete", "dir", dir, "files_ingested", scanned)
		}
	}

	// Batch state save after startup scan completes
	if w.cfg.WatchStateFile != "" {
		if saveErr := w.state.Save(w.cfg.WatchStateFile); saveErr != nil {
			slog.Warn("failed to save state after startup scan", "error", saveErr)
		}
	}

	// Add directories to fsnotify
	for _, dir := range valid {
		if addErr := w.fsw.Add(dir); addErr != nil {
			slog.Warn("failed to watch directory", "dir", dir, "error", addErr)
		} else {
			slog.Info("watching directory", "dir", dir)
		}
	}

	slog.Info("watcher started", "dirs", len(valid))

	return w.eventLoop(ctx)
}

// eventLoop processes fsnotify events until ctx is cancelled.
func (w *Watcher) eventLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			w.cancelPending()
			return w.fsw.Close()

		case event, ok := <-w.fsw.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Create|fsnotify.Write) == 0 {
				continue
			}
			w.handleEvent(ctx, event.Name)

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return nil
			}
			slog.Warn("fsnotify error", "error", err)
		}
	}
}

// handleEvent filters and debounces a file event.
func (w *Watcher) handleEvent(ctx context.Context, filePath string) {
	name := filepath.Base(filePath)

	if isTempFile(name) {
		slog.Debug("skipping temp file", "path", filePath)
		return
	}

	if !isSupportedFormat(name) {
		slog.Debug("skipping unsupported format", "path", filePath)
		return
	}

	w.scheduleIngest(ctx, filePath)
}

// scheduleIngest debounces ingestion for a file path. Subsequent calls within
// the debounce window reset the timer.
func (w *Watcher) scheduleIngest(ctx context.Context, filePath string) {
	w.pendingLock.Lock()
	defer w.pendingLock.Unlock()

	if timer, ok := w.pending[filePath]; ok {
		timer.Stop()
	}

	w.pending[filePath] = time.AfterFunc(time.Duration(w.debounceMs)*time.Millisecond, func() {
		// Acquire semaphore slot for bounded concurrency
		w.sem <- struct{}{}
		defer func() { <-w.sem }()
		w.doIngest(ctx, filePath)
	})
}

// doIngest performs the actual ingestion of a single file.
func (w *Watcher) doIngest(ctx context.Context, filePath string) {
	// Check context before doing any work (debounce ctx check)
	if ctx.Err() != nil {
		return
	}

	w.pendingLock.Lock()
	delete(w.pending, filePath)
	w.pendingLock.Unlock()

	// TOCTOU mitigation: use Lstat to detect symlinks before ingestion
	info, err := os.Lstat(filePath)
	if err != nil {
		slog.Warn("cannot lstat file for ingestion", "path", filePath, "error", err)
		return
	}

	// Reject symlinks — only ingest regular files
	if info.Mode()&os.ModeSymlink != 0 {
		slog.Warn("skipping symlink", "path", filePath)
		return
	}

	if !info.Mode().IsRegular() {
		slog.Warn("skipping non-regular file", "path", filePath)
		return
	}

	if !w.state.ShouldIngest(filePath, info.ModTime()) {
		slog.Debug("skipping unchanged file", "path", filePath)
		return
	}

	meta := w.buildAutoTagMeta(filePath)
	result, err := w.ingester.IngestFile(ctx, filePath, "watchd", meta)
	if err != nil {
		slog.Warn("ingestion failed", "path", filePath, "error", err)
		return
	}

	w.state.MarkIngested(filePath, info.ModTime())
	slog.Info("ingested file", "path", filePath, "result", result)

	// Persist state after each successful ingestion
	if w.cfg.WatchStateFile != "" {
		if saveErr := w.state.Save(w.cfg.WatchStateFile); saveErr != nil {
			slog.Warn("failed to save state", "error", saveErr)
		}
	}
}

// cancelPending stops all pending debounce timers.
func (w *Watcher) cancelPending() {
	w.pendingLock.Lock()
	defer w.pendingLock.Unlock()
	for path, timer := range w.pending {
		timer.Stop()
		delete(w.pending, path)
	}
}

// ScanDir performs a startup scan of a single directory, ingesting any files
// that haven't been ingested yet or have changed since last ingestion.
// Returns the number of files ingested. Respects ctx cancellation.
// State is NOT saved per-file; the caller is responsible for batch-saving
// after the scan completes.
func (w *Watcher) ScanDir(ctx context.Context, dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read dir %s: %w", dir, err)
	}

	ingested := 0
	scanned := 0

	for _, entry := range entries {
		// Check context in scan loop
		if ctx.Err() != nil {
			return ingested, ctx.Err()
		}

		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if isTempFile(name) || !isSupportedFormat(name) {
			continue
		}

		filePath := filepath.Join(dir, name)

		// TOCTOU mitigation: use Lstat to reject symlinks during scan
		fileInfo, err := os.Lstat(filePath)
		if err != nil {
			slog.Warn("cannot lstat file during scan", "path", filePath, "error", err)
			continue
		}
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			slog.Warn("skipping symlink during scan", "path", filePath)
			continue
		}
		if !fileInfo.Mode().IsRegular() {
			continue
		}

		if !w.state.ShouldIngest(filePath, fileInfo.ModTime()) {
			continue
		}

		// Rate limit: pause briefly every 10 files to avoid overwhelming
		scanned++
		if scanned > 1 && scanned%10 == 0 {
			time.Sleep(100 * time.Millisecond)
		}

		// Acquire semaphore slot for bounded concurrency
		w.sem <- struct{}{}

		meta := w.buildAutoTagMeta(filePath)
		result, ingestErr := w.ingester.IngestFile(ctx, filePath, "watchd-scan", meta)

		<-w.sem // release semaphore

		if ingestErr != nil {
			slog.Warn("scan ingestion failed", "path", filePath, "error", ingestErr)
			continue
		}

		w.state.MarkIngested(filePath, fileInfo.ModTime())
		slog.Info("scan ingested file", "path", filePath, "result", result)
		ingested++
	}

	return ingested, nil
}

// buildAutoTagMeta computes folder-based auto tags for the given file and
// returns a metadata map suitable for passing to the ingester.
func (w *Watcher) buildAutoTagMeta(filePath string) map[string]any {
	dirs := ParseWatchDirs(w.cfg.WatchDirs)
	watchRoot := findWatchRoot(dirs, filePath)
	if watchRoot == "" {
		return nil
	}
	tags := FolderTags(filePath, watchRoot)
	if len(tags) == 0 {
		return nil
	}
	return map[string]any{"auto_tags": tags}
}

// findWatchRoot returns the configured watch directory that contains filePath.
func findWatchRoot(dirs []string, filePath string) string {
	cleanPath := filepath.Clean(filePath)
	for _, dir := range dirs {
		cleanDir := filepath.Clean(dir)
		if strings.HasPrefix(cleanPath, cleanDir+string(filepath.Separator)) {
			return cleanDir
		}
	}
	return ""
}
