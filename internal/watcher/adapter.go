package watcher

import (
	"context"

	"github.com/craig8/openbrain/internal/brain"
)

// BrainAdapter wraps a *brain.Brain to satisfy the Ingester interface.
type BrainAdapter struct {
	brain *brain.Brain
}

// NewBrainAdapter creates an adapter that delegates to Brain.IngestDocument.
func NewBrainAdapter(b *brain.Brain) *BrainAdapter {
	return &BrainAdapter{brain: b}
}

// IngestFile delegates to Brain.IngestDocument with autoCapture enabled,
// forwarding any caller-supplied metadata (e.g. auto_tags from folder watcher).
func (a *BrainAdapter) IngestFile(ctx context.Context, filePath, source string, metadata map[string]any) (string, error) {
	return a.brain.IngestDocument(ctx, filePath, source, true, metadata)
}
