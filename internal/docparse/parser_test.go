package docparse

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    Format
		wantErr bool
	}{
		{"pdf lowercase", "report.pdf", FormatPDF, false},
		{"pdf uppercase", "REPORT.PDF", FormatPDF, false},
		{"png image", "screenshot.png", FormatOCR, false},
		{"jpg image", "photo.jpg", FormatOCR, false},
		{"jpeg image", "photo.jpeg", FormatOCR, false},
		{"tiff image", "scan.tiff", FormatOCR, false},
		{"tif image", "scan.tif", FormatOCR, false},
		{"bmp image", "image.bmp", FormatOCR, false},
		{"docx file", "document.docx", FormatDOCX, false},
		{"DOCX uppercase", "DOC.DOCX", FormatDOCX, false},
		{"txt file", "notes.txt", FormatText, false},
		{"unsupported doc", "old.doc", "", true},
		{"no extension", "readme", "", true},
		{"path with dirs", "/home/user/docs/report.pdf", FormatPDF, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetectFormat(tt.path)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseResult_HasRequiredFields(t *testing.T) {
	r := ParseResult{
		Text: "extracted content",
		Metadata: map[string]any{
			"filename":        "test.pdf",
			"format":          "pdf",
			"file_size_bytes": int64(1024),
		},
	}

	assert.Equal(t, "extracted content", r.Text)
	assert.Equal(t, "test.pdf", r.Metadata["filename"])
	assert.Equal(t, "pdf", r.Metadata["format"])
	assert.Equal(t, int64(1024), r.Metadata["file_size_bytes"])
}
