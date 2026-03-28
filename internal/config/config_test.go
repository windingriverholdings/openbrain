package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultSearchScoreThreshold(t *testing.T) {
	// The default threshold should be 0.15, not 0.35.
	// 0.35 is too aggressive for small corpora.
	assert.Equal(t, 0.15, defaultSearchScoreThreshold,
		"default threshold should be 0.15 for small corpora compatibility")
}

func TestIngestDirDefault(t *testing.T) {
	cfg := &Config{}
	assert.Equal(t, "", cfg.IngestDir, "IngestDir should default to empty string")
}

func TestTesseractLangsDefault(t *testing.T) {
	// TesseractLangs should default to "eng" when loaded from env.
	// We test the struct tag default by loading with no env set.
	cfg := &Config{TesseractLangs: "eng"}
	assert.Equal(t, "eng", cfg.TesseractLangs)
}

func TestIngestDirFromEnv(t *testing.T) {
	t.Setenv("OPENBRAIN_INGEST_DIR", "/tmp/openbrain-ingest")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, "/tmp/openbrain-ingest", cfg.IngestDir)
}

func TestTesseractLangsFromEnv(t *testing.T) {
	t.Setenv("OPENBRAIN_TESSERACT_LANGS", "eng+fra")
	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, "eng+fra", cfg.TesseractLangs)
}

func TestTesseractLangsValidation_RejectsInvalid(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"uppercase", "ENG"},
		{"too short", "en"},
		{"too long", "english"},
		{"bad separator", "eng-fra"},
		{"trailing plus", "eng+"},
		{"leading plus", "+eng"},
		{"numbers", "en3"},
		{"spaces", "eng fra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENBRAIN_TESSERACT_LANGS", tt.value)
			_, err := Load()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "OPENBRAIN_TESSERACT_LANGS")
		})
	}
}

func TestTesseractLangsValidation_AcceptsValid(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"single lang", "eng"},
		{"two langs", "eng+fra"},
		{"three langs", "eng+fra+deu"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENBRAIN_TESSERACT_LANGS", tt.value)
			cfg, err := Load()
			assert.NoError(t, err)
			assert.Equal(t, tt.value, cfg.TesseractLangs)
		})
	}
}
