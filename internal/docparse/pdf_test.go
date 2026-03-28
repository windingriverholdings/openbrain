package docparse

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPDFParser_Parse(t *testing.T) {
	p := &pdfParser{}
	result, err := p.Parse(context.Background(), "testdata/sample.pdf")
	require.NoError(t, err)

	assert.Contains(t, result.Text, "Hello OpenBrain")
	assert.Equal(t, "sample.pdf", result.Metadata["filename"])
	assert.Equal(t, "pdf", result.Metadata["format"])
	assert.Equal(t, 1, result.Metadata["page_count"])
	assert.Greater(t, result.Metadata["file_size_bytes"].(int64), int64(0))
}

func TestPDFParser_MissingFile(t *testing.T) {
	p := &pdfParser{}
	_, err := p.Parse(context.Background(), "testdata/nonexistent.pdf")
	assert.Error(t, err)
}
