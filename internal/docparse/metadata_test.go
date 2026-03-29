package docparse

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/craig8/openbrain/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetadataConstants_Values(t *testing.T) {
	// Verify constants have the expected string values.
	assert.Equal(t, "source_file", MetaSourceFile)
	assert.Equal(t, "source_path", MetaSourcePath)
	assert.Equal(t, "source_format", MetaSourceFormat)
	assert.Equal(t, "chunk_index", MetaChunkIndex)
	assert.Equal(t, "chunk_total", MetaChunkTotal)
	assert.Equal(t, "page_number", MetaPageNumber)
	assert.Equal(t, "ingested_at", MetaIngestedAt)
}

func TestPDFParser_PopulatesSourceFormat(t *testing.T) {
	p := &pdfParser{}
	result, err := p.Parse(context.Background(), "testdata/sample.pdf")
	require.NoError(t, err)

	// Parser should populate source_format using the MetaSourceFormat constant.
	sf, ok := result.Metadata[MetaSourceFormat]
	assert.True(t, ok, "metadata should contain %s key", MetaSourceFormat)
	assert.Equal(t, "pdf", sf)
}

func TestTextParser_PopulatesSourceFormat(t *testing.T) {
	cfg := &config.Config{IngestMaxBytes: config.DefaultIngestMaxBytes}
	p, err := NewParser(FormatText, cfg)
	require.NoError(t, err)

	result, err := p.Parse(context.Background(), filepath.Join("testdata", "sample.txt"))
	require.NoError(t, err)

	sf, ok := result.Metadata[MetaSourceFormat]
	assert.True(t, ok, "metadata should contain %s key", MetaSourceFormat)
	assert.Equal(t, "text", sf)
}

func TestDOCXParser_PopulatesSourceFormat(t *testing.T) {
	p := &docxParser{}
	result, err := p.Parse(context.Background(), "testdata/sample.docx")
	require.NoError(t, err)

	sf, ok := result.Metadata[MetaSourceFormat]
	assert.True(t, ok, "metadata should contain %s key", MetaSourceFormat)
	assert.Equal(t, "docx", sf)
}

func TestMarkitdownParser_PopulatesSourceFormat(t *testing.T) {
	// Use a stub script that outputs text, simulating markitdown.
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-markitdown.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho 'slide content'\n"), 0755))

	p := &markitdownParser{binPath: script, maxBytes: config.DefaultIngestMaxBytes}

	dummy := filepath.Join(dir, "test.pptx")
	require.NoError(t, os.WriteFile(dummy, []byte("fake"), 0644))

	result, err := p.Parse(context.Background(), dummy)
	require.NoError(t, err)

	sf, ok := result.Metadata[MetaSourceFormat]
	assert.True(t, ok, "metadata should contain %s key", MetaSourceFormat)
	assert.Equal(t, "pptx", sf)
}

func TestMarkitdownParser_XLSXSourceFormat(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-markitdown.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho 'sheet data'\n"), 0755))

	p := &markitdownParser{binPath: script, maxBytes: config.DefaultIngestMaxBytes}

	dummy := filepath.Join(dir, "data.xlsx")
	require.NoError(t, os.WriteFile(dummy, []byte("fake"), 0644))

	result, err := p.Parse(context.Background(), dummy)
	require.NoError(t, err)

	sf, ok := result.Metadata[MetaSourceFormat]
	assert.True(t, ok, "metadata should contain %s key", MetaSourceFormat)
	assert.Equal(t, "xlsx", sf)
}
