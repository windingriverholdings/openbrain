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

func (m *mockBrain) IngestFile(ctx context.Context, filePath, source string, metadata map[string]any) (string, error) {
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

	ctx := context.Background()
	scanned, err := w.ScanDir(ctx, dir)
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

	ctx := context.Background()
	scanned, err := w.ScanDir(ctx, dir)
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
	ingestDir := t.TempDir()
	dirs := []string{"/nonexistent/dir/12345"}
	valid, err := validateWatchDirs(dirs, ingestDir)
	require.NoError(t, err)
	assert.Empty(t, valid)
}

func TestValidateWatchDirs_RejectsFiles(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "not-a-dir.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hi"), 0644))

	valid, err := validateWatchDirs([]string{filePath}, dir)
	require.NoError(t, err)
	assert.Empty(t, valid)
}

func TestValidateWatchDirs_AcceptsValidDir(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0755))

	valid, err := validateWatchDirs([]string{subDir}, dir)
	require.NoError(t, err)
	assert.Equal(t, []string{subDir}, valid)
}

func TestValidateWatchDirs_AcceptsIngestDirItself(t *testing.T) {
	dir := t.TempDir()
	valid, err := validateWatchDirs([]string{dir}, dir)
	require.NoError(t, err)
	assert.Equal(t, []string{dir}, valid)
}

func TestValidateWatchDirs_RejectsOutsideIngestDir(t *testing.T) {
	ingestDir := t.TempDir()
	outsideDir := t.TempDir()

	_, err := validateWatchDirs([]string{outsideDir}, ingestDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside IngestDir")
}

func TestValidateWatchDirs_RejectsEmptyIngestDir(t *testing.T) {
	dir := t.TempDir()
	_, err := validateWatchDirs([]string{dir}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OPENBRAIN_INGEST_DIR must be set")
}

func TestValidateWatchDirs_RejectsSymlinkEscape(t *testing.T) {
	ingestDir := t.TempDir()
	outsideDir := t.TempDir()

	// Create symlink inside ingestDir pointing to outsideDir
	symlinkPath := filepath.Join(ingestDir, "sneaky-link")
	err := os.Symlink(outsideDir, symlinkPath)
	require.NoError(t, err)

	_, err = validateWatchDirs([]string{symlinkPath}, ingestDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside IngestDir")
}

func TestScanDir_RespectsContextCancellation(t *testing.T) {
	dir := t.TempDir()

	// Create several files
	for i := 0; i < 5; i++ {
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, fmt.Sprintf("file%d.md", i)),
			[]byte("content"), 0644,
		))
	}

	mb := &mockBrain{}
	state := NewState()
	cfg := &config.Config{
		WatchDirs:       dir,
		WatchDebounceMs: 100,
		IngestDir:       dir,
	}

	w, err := New(mb, cfg, state)
	require.NoError(t, err)

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, scanErr := w.ScanDir(ctx, dir)
	assert.ErrorIs(t, scanErr, context.Canceled)
}

func TestDoIngest_SkipsSymlinks(t *testing.T) {
	dir := t.TempDir()

	// Create a real file and a symlink to it
	realFile := filepath.Join(dir, "real.md")
	require.NoError(t, os.WriteFile(realFile, []byte("content"), 0644))

	symlinkFile := filepath.Join(dir, "link.md")
	require.NoError(t, os.Symlink(realFile, symlinkFile))

	mb := &mockBrain{}
	state := NewState()
	cfg := &config.Config{
		WatchDirs:       dir,
		WatchDebounceMs: 100,
		IngestDir:       dir,
	}

	w, err := New(mb, cfg, state)
	require.NoError(t, err)

	ctx := context.Background()

	// Ingest the symlink — should be rejected
	w.doIngest(ctx, symlinkFile)

	files := mb.ingestedFiles()
	assert.Empty(t, files, "symlink should not be ingested")
}

func TestDoIngest_SkipsWhenContextCancelled(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "note.md")
	require.NoError(t, os.WriteFile(filePath, []byte("content"), 0644))

	mb := &mockBrain{}
	state := NewState()
	cfg := &config.Config{
		WatchDirs:       dir,
		WatchDebounceMs: 100,
		IngestDir:       dir,
	}

	w, err := New(mb, cfg, state)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	w.doIngest(ctx, filePath)

	files := mb.ingestedFiles()
	assert.Empty(t, files, "cancelled context should prevent ingestion")
}

func TestIngestionFailure_DoesNotMarkState(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "note.md")
	require.NoError(t, os.WriteFile(filePath, []byte("content"), 0644))

	mb := &mockBrain{err: fmt.Errorf("simulated ingestion failure")}
	state := NewState()
	cfg := &config.Config{
		WatchDirs:       dir,
		WatchDebounceMs: 100,
		IngestDir:       dir,
	}

	w, err := New(mb, cfg, state)
	require.NoError(t, err)

	ctx := context.Background()
	w.doIngest(ctx, filePath)

	// File should NOT be marked as ingested
	info, err := os.Lstat(filePath)
	require.NoError(t, err)
	assert.True(t, state.ShouldIngest(filePath, info.ModTime()),
		"failed ingestion should not mark file in state")
}
