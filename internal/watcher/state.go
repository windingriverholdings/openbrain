// Package watcher provides a filesystem watcher daemon that auto-ingests
// documents into OpenBrain when files are created or modified in watched
// directories.
package watcher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State tracks which files have been ingested by path and modification time.
// It is safe for concurrent use.
type State struct {
	mu    sync.RWMutex
	Files map[string]time.Time `json:"files"`
}

// NewState creates an empty State.
func NewState() *State {
	return &State{
		Files: make(map[string]time.Time),
	}
}

// ShouldIngest returns true if the file has not been ingested at the given mtime.
func (s *State) ShouldIngest(path string, mtime time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prev, ok := s.Files[path]
	if !ok {
		return true
	}
	return !mtime.Equal(prev)
}

// MarkIngested records that the file at path was ingested at the given mtime.
func (s *State) MarkIngested(path string, mtime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Files[path] = mtime
}

// Save persists state to a JSON file atomically (write to .tmp then rename).
// Creates parent directories if needed.
func (s *State) Save(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write temp state file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		// Clean up tmp on rename failure
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename state file: %w", err)
	}

	return nil
}

// LoadState reads state from a JSON file. Returns empty state if file does not exist.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewState(), nil
		}
		return nil, fmt.Errorf("read state file: %w", err)
	}

	s := NewState()
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("unmarshal state: %w", err)
	}

	if s.Files == nil {
		s.Files = make(map[string]time.Time)
	}

	return s, nil
}
