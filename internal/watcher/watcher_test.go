package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/craig8/openbrain/internal/config"
)

// mockBrain records IngestDocument calls without needing a real DB.
type mockBrain struct {
	mu       sync.Mutex
	ingested []string
	err      error
}

func (m *mockBrain) IngestFile(ctx context.Context, filePath, source string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return "", m.err
	}
	m.ingested = append(m.ingested, filePath)
	return fmt.Sprintf("ingested %s", filepath.Base(filePath)), nil
}

func (m *mockBrain) ingestedFiles() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.ingested))
	copy(result, m.ingested)
	return result
}

func TestIsTempFile(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		{"regular md", "notes.md", false},
		{"regular txt", "doc.txt", false},
		{"tmp suffix", "file.tmp", true},
		{"tilde suffix", "file.md~", true},
		{"swp suffix", "file.swp", true},
		{"part suffix", "download.part", true},
		{"crdownload suffix", "file.crdownload", true},
		{"dot prefix", ".hidden.md", false},
		{"vim swap", ".file.swp", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isTempFile(tt.filename))
		})
	}
}

func TestIsSupportedFormat(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		{"markdown", "notes.md", true},
		{"text", "doc.txt", true},
		{"pdf", "report.pdf", true},
		{"docx", "letter.docx", true},
		{"go source", "main.go", true},
		{"python", "script.py", true},
		{"binary", "program.exe", false},
		{"zip", "archive.zip", false},
		{"no extension", "README", false},
		{"pptx", "slides.pptx", true},
		{"xlsx", "data.xlsx", true},
		{"json", "config.json", true},
		{"yaml", "spec.yaml", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isSupportedFormat(tt.filename))
		})
	}
}

func TestStartupScan(t *testing.T) {
	dir := t.TempDir()

	// Create test files
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.md"), []byte("hello"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "doc.txt"), []byte("world"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "temp.tmp"), []byte("skip me"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prog.exe"), []byte("binary"), 0644))

	mb := &mockBrain{}
	state := NewState()
	cfg := &config.Config{
		WatchDirs:       dir,
		WatchDebounceMs: 100,
		IngestDir:       dir,
	}

	w, err := New(mb, cfg, state)
	require.NoError(t, err)

	scanned, err := w.ScanDir(dir)
	require.NoError(t, err)
	// Should find note.md and doc.txt, skip temp.tmp and prog.exe
	assert.Equal(t, 2, scanned)
}

func TestStartupScan_SkipsAlreadyIngested(t *testing.T) {
	dir := t.TempDir()

	filePath := filepath.Join(dir, "note.md")
	require.NoError(t, os.WriteFile(filePath, []byte("hello"), 0644))

	info, err := os.Stat(filePath)
	require.NoError(t, err)

	mb := &mockBrain{}
	state := NewState()
	state.MarkIngested(filePath, info.ModTime())

	cfg := &config.Config{
		WatchDirs:       dir,
		WatchDebounceMs: 100,
		IngestDir:       dir,
	}

	w, err := New(mb, cfg, state)
	require.NoError(t, err)

	scanned, err := w.ScanDir(dir)
	require.NoError(t, err)
	assert.Equal(t, 0, scanned)
	assert.Empty(t, mb.ingestedFiles())
}

func TestDebounce_RapidEventsProduceSingleIngestion(t *testing.T) {
	dir := t.TempDir()

	mb := &mockBrain{}
	state := NewState()
	cfg := &config.Config{
		WatchDirs:       dir,
		WatchDebounceMs: 200,
		IngestDir:       dir,
	}

	w, err := New(mb, cfg, state)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	filePath := filepath.Join(dir, "note.md")
	require.NoError(t, os.WriteFile(filePath, []byte("hello"), 0644))

	// Simulate rapid writes (3 events within debounce window)
	for i := 0; i < 3; i++ {
		w.scheduleIngest(ctx, filePath)
		time.Sleep(50 * time.Millisecond) // well within 200ms debounce
	}

	// Wait for debounce to fire
	time.Sleep(400 * time.Millisecond)

	files := mb.ingestedFiles()
	assert.Len(t, files, 1, "rapid events should produce single ingestion")
	assert.Equal(t, filePath, files[0])
}

func TestValidateWatchDirs_RejectsNonexistent(t *testing.T) {
	dirs := []string{"/nonexistent/dir/12345"}
	valid := validateWatchDirs(dirs)
	assert.Empty(t, valid)
}

func TestValidateWatchDirs_RejectsFiles(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "not-a-dir.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hi"), 0644))

	valid := validateWatchDirs([]string{filePath})
	assert.Empty(t, valid)
}

func TestValidateWatchDirs_AcceptsValidDir(t *testing.T) {
	dir := t.TempDir()
	valid := validateWatchDirs([]string{dir})
	assert.Equal(t, []string{dir}, valid)
}
