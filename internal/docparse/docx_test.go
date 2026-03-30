package docparse

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDOCXParser_Parse(t *testing.T) {
	p := &docxParser{}
	result, err := p.Parse(context.Background(), "testdata/sample.docx")
	require.NoError(t, err)

	assert.Contains(t, result.Text, "Hello from OpenBrain")
	assert.Contains(t, result.Text, "three paragraphs")
	assert.Equal(t, "sample.docx", result.Metadata["filename"])
	assert.Equal(t, "docx", result.Metadata["format"])
	assert.Equal(t, 3, result.Metadata["paragraph_count"])
	assert.Greater(t, result.Metadata["file_size_bytes"].(int64), int64(0))
}

func TestDOCXParser_MissingFile(t *testing.T) {
	p := &docxParser{}
	_, err := p.Parse(context.Background(), "testdata/nonexistent.docx")
	assert.Error(t, err)
}
