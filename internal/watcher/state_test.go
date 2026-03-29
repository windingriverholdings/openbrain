package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewState(t *testing.T) {
	s := NewState()
	assert.NotNil(t, s)
	assert.Empty(t, s.Files)
}

func TestShouldIngest_NewFile(t *testing.T) {
	s := NewState()
	mtime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	assert.True(t, s.ShouldIngest("/tmp/test.md", mtime))
}

func TestShouldIngest_UnchangedFile(t *testing.T) {
	s := NewState()
	mtime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	s.MarkIngested("/tmp/test.md", mtime)
	assert.False(t, s.ShouldIngest("/tmp/test.md", mtime))
}

func TestShouldIngest_ModifiedFile(t *testing.T) {
	s := NewState()
	oldMtime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	s.MarkIngested("/tmp/test.md", oldMtime)

	newMtime := time.Date(2025, 6, 15, 11, 0, 0, 0, time.UTC)
	assert.True(t, s.ShouldIngest("/tmp/test.md", newMtime))
}

func TestMarkIngested(t *testing.T) {
	s := NewState()
	mtime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	s.MarkIngested("/tmp/test.md", mtime)

	assert.Contains(t, s.Files, "/tmp/test.md")
	assert.Equal(t, mtime, s.Files["/tmp/test.md"])
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	s := NewState()
	mtime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	s.MarkIngested("/tmp/test.md", mtime)
	s.MarkIngested("/tmp/notes.txt", mtime.Add(time.Hour))

	err := s.Save(statePath)
	require.NoError(t, err)

	loaded, err := LoadState(statePath)
	require.NoError(t, err)
	assert.Len(t, loaded.Files, 2)
	assert.Equal(t, mtime, loaded.Files["/tmp/test.md"])
	assert.Equal(t, mtime.Add(time.Hour), loaded.Files["/tmp/notes.txt"])
}

func TestLoadState_FileNotFound(t *testing.T) {
	s, err := LoadState("/nonexistent/path/state.json")
	require.NoError(t, err, "missing state file should return empty state, not error")
	assert.Empty(t, s.Files)
}

func TestSaveState_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "subdir", "state.json")

	s := NewState()
	s.MarkIngested("/tmp/test.md", time.Now())
	err := s.Save(statePath)
	require.NoError(t, err)

	_, err = os.Stat(statePath)
	assert.NoError(t, err)
}
