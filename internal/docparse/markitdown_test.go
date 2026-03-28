package docparse

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/craig8/openbrain/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectFormat_PPTX(t *testing.T) {
	got, err := DetectFormat("slides.pptx")
	require.NoError(t, err)
	assert.Equal(t, FormatPPTX, got)
}

func TestDetectFormat_XLSX(t *testing.T) {
	got, err := DetectFormat("data.xlsx")
	require.NoError(t, err)
	assert.Equal(t, FormatXLSX, got)
}

func TestDetectFormat_PPTX_Uppercase(t *testing.T) {
	got, err := DetectFormat("SLIDES.PPTX")
	require.NoError(t, err)
	assert.Equal(t, FormatPPTX, got)
}

func TestDetectFormat_XLSX_Uppercase(t *testing.T) {
	got, err := DetectFormat("DATA.XLSX")
	require.NoError(t, err)
	assert.Equal(t, FormatXLSX, got)
}

func TestNewParser_PPTX_ReturnsMarkitdownParser(t *testing.T) {
	cfg := &config.Config{}
	p, err := NewParser(FormatPPTX, cfg)
	require.NoError(t, err)

	mp, ok := p.(*markitdownParser)
	require.True(t, ok, "expected *markitdownParser, got %T", p)
	assert.Equal(t, "markitdown", mp.binPath)
}

func TestNewParser_XLSX_ReturnsMarkitdownParser(t *testing.T) {
	cfg := &config.Config{}
	p, err := NewParser(FormatXLSX, cfg)
	require.NoError(t, err)

	mp, ok := p.(*markitdownParser)
	require.True(t, ok, "expected *markitdownParser, got %T", p)
	assert.Equal(t, "markitdown", mp.binPath)
}

func TestNewParser_CustomMarkitdownPath(t *testing.T) {
	cfg := &config.Config{MarkitdownPath: "/usr/local/bin/markitdown"}
	p, err := NewParser(FormatPPTX, cfg)
	require.NoError(t, err)

	mp, ok := p.(*markitdownParser)
	require.True(t, ok)
	assert.Equal(t, "/usr/local/bin/markitdown", mp.binPath)
}

func TestMarkitdownParser_BinaryNotFound(t *testing.T) {
	p := &markitdownParser{binPath: "/nonexistent/markitdown-fake-bin"}

	dir := t.TempDir()
	dummy := filepath.Join(dir, "test.pptx")
	require.NoError(t, os.WriteFile(dummy, []byte("fake"), 0644))

	_, err := p.Parse(context.Background(), dummy)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "markitdown binary not found")
}

func TestMarkitdownParser_NonZeroExit(t *testing.T) {
	// "false" always exits with code 1.
	p := &markitdownParser{binPath: "false"}

	dir := t.TempDir()
	dummy := filepath.Join(dir, "test.xlsx")
	require.NoError(t, os.WriteFile(dummy, []byte("fake"), 0644))

	_, err := p.Parse(context.Background(), dummy)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "markitdown exited with error")
}

func TestMarkitdownParser_ContextCancellation(t *testing.T) {
	// "sleep" as the binary so cancellation fires before it completes.
	p := &markitdownParser{binPath: "sleep"}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	dir := t.TempDir()
	dummy := filepath.Join(dir, "test.pptx")
	require.NoError(t, os.WriteFile(dummy, []byte("fake"), 0644))

	_, err := p.Parse(ctx, dummy)
	require.Error(t, err)
}

func TestMarkitdownParser_StdoutSizeLimit(t *testing.T) {
	// Script that outputs more bytes than the configured limit.
	dir := t.TempDir()
	script := filepath.Join(dir, "big-output.sh")
	scriptContent := "#!/bin/sh\ndd if=/dev/zero bs=1024 count=200 2>/dev/null | tr '\\0' 'A'\n"
	require.NoError(t, os.WriteFile(script, []byte(scriptContent), 0755))

	p := &markitdownParser{binPath: script, maxBytes: 1024}

	dummy := filepath.Join(dir, "test.pptx")
	require.NoError(t, os.WriteFile(dummy, []byte("fake"), 0644))

	_, err := p.Parse(context.Background(), dummy)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestMarkitdownParser_StderrTruncation(t *testing.T) {
	// Script that writes a long stderr message and exits non-zero.
	dir := t.TempDir()
	script := filepath.Join(dir, "long-stderr.sh")
	scriptContent := "#!/bin/sh\nprintf '%0.s_' $(seq 1 500) >&2; exit 1\n"
	require.NoError(t, os.WriteFile(script, []byte(scriptContent), 0755))

	p := &markitdownParser{binPath: script}

	dummy := filepath.Join(dir, "test.pptx")
	require.NoError(t, os.WriteFile(dummy, []byte("fake"), 0644))

	_, err := p.Parse(context.Background(), dummy)
	require.Error(t, err)
	errMsg := err.Error()
	assert.Contains(t, errMsg, "stderr:")
	// Full 500-char stderr should be truncated to 256 chars max.
	assert.LessOrEqual(t, len(errMsg), 512, "error message should be bounded by stderr truncation")
}
