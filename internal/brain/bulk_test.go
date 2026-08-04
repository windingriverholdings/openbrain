package brain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/windingriverholdings/openbrain/internal/config"
	"github.com/windingriverholdings/openbrain/internal/db"
	"github.com/windingriverholdings/openbrain/internal/embeddings"
	"github.com/windingriverholdings/openbrain/internal/extract"
	"github.com/windingriverholdings/openbrain/internal/intent"
)

// fakeBulkInserter records the inputs it received and returns a scripted
// outcome, so BulkImport's atomicity and fail-loud contract is testable without
// a live database.
type fakeBulkInserter struct {
	called bool
	got    []db.ThoughtInput
	ids    []string
	err    error
}

func (f *fakeBulkInserter) insert(_ context.Context, inputs []db.ThoughtInput) ([]string, error) {
	f.called = true
	f.got = inputs
	if f.err != nil {
		return nil, f.err
	}
	ids := f.ids
	if ids == nil {
		ids = make([]string, len(inputs))
		for i := range inputs {
			ids[i] = padID(i+1, inputs[i].Content)
		}
	}
	return ids, nil
}

func newBulkBrain(fake *fakeBulkInserter, emb embeddings.Embedder) *Brain {
	b := &Brain{embedder: emb}
	b.bulkInsertFn = fake.insert
	return b
}

// TestBulkImport_HappyPath asserts a well-formed batch is embedded, handed to
// the atomic inserter in one call, and the ids come back in input order.
func TestBulkImport_HappyPath(t *testing.T) {
	fake := &fakeBulkInserter{}
	b := newBulkBrain(fake, staticEmbedder{})

	items := []BulkItem{
		{Content: "first thought", ThoughtType: "note"},
		{Content: "second thought", ThoughtType: "insight"},
	}
	ids, err := b.BulkImport(context.Background(), items, "import")
	require.NoError(t, err)
	require.Len(t, ids, 2)
	assert.True(t, fake.called, "the atomic inserter must be called exactly once for the batch")
	require.Len(t, fake.got, 2, "every item must be handed to the single inserter call")
	assert.Equal(t, "first thought", fake.got[0].Content)
	assert.Equal(t, "insight", fake.got[1].ThoughtType)
	assert.Equal(t, "import", fake.got[0].Source)
}

// TestBulkImport_MalformedItemAbortsBatch asserts a blank-content item aborts
// the whole batch with a typed error BEFORE any write, so no partial batch is
// ever handed to the inserter.
func TestBulkImport_MalformedItemAbortsBatch(t *testing.T) {
	fake := &fakeBulkInserter{}
	b := newBulkBrain(fake, staticEmbedder{})

	items := []BulkItem{
		{Content: "valid", ThoughtType: "note"},
		{Content: "   ", ThoughtType: "note"},
	}
	ids, err := b.BulkImport(context.Background(), items, "import")
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.True(t, errors.Is(err, ErrEmptyContent), "expected ErrEmptyContent, got %v", err)
	assert.False(t, fake.called, "no write may be attempted when an item is malformed")
}

// TestBulkImport_EmbedFailureAborts asserts an embed failure on any item aborts
// the whole batch loudly, with no write attempted.
func TestBulkImport_EmbedFailureAborts(t *testing.T) {
	fake := &fakeBulkInserter{}
	b := newBulkBrain(fake, erroringEmbedder{failOn: "boom"})

	items := []BulkItem{
		{Content: "fine", ThoughtType: "note"},
		{Content: "boom", ThoughtType: "note"},
	}
	ids, err := b.BulkImport(context.Background(), items, "import")
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.Contains(t, err.Error(), "embed")
	assert.False(t, fake.called, "no write may be attempted when embedding fails")
}

// TestBulkImport_StoreFailureIsLoud asserts a store-step failure surfaces as a
// real, typed error, never a success-shaped summary string.
func TestBulkImport_StoreFailureIsLoud(t *testing.T) {
	fake := &fakeBulkInserter{err: db.ErrEmptyBatch}
	b := newBulkBrain(fake, staticEmbedder{})

	items := []BulkItem{{Content: "will fail to store", ThoughtType: "note"}}
	ids, err := b.BulkImport(context.Background(), items, "import")
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.True(t, errors.Is(err, db.ErrEmptyBatch), "underlying store error must propagate, got %v", err)
}

// TestBulkImport_EmptyBatch asserts an empty batch is a typed error.
func TestBulkImport_EmptyBatch(t *testing.T) {
	fake := &fakeBulkInserter{}
	b := newBulkBrain(fake, staticEmbedder{})

	ids, err := b.BulkImport(context.Background(), nil, "import")
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.True(t, errors.Is(err, db.ErrEmptyBatch))
	assert.False(t, fake.called)
}

// TestBulkImport_OverCapBatchRejected asserts a batch over the configured
// item cap is refused with a typed error BEFORE any embed call, and nothing
// is handed to the atomic inserter.
func TestBulkImport_OverCapBatchRejected(t *testing.T) {
	fake := &fakeBulkInserter{}
	b := newBulkBrain(fake, staticEmbedder{})
	b.cfg = &config.Config{BulkImportMaxItems: 2}

	items := []BulkItem{
		{Content: "one", ThoughtType: "note"},
		{Content: "two", ThoughtType: "note"},
		{Content: "three", ThoughtType: "note"},
	}
	ids, err := b.BulkImport(context.Background(), items, "import")
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.True(t, errors.Is(err, ErrBatchTooLarge), "expected ErrBatchTooLarge, got %v", err)
	assert.False(t, fake.called, "no write may be attempted when the batch exceeds the item cap")
}

// TestBulkImport_OverCapContentRejected asserts an item whose content exceeds
// the configured per-item length cap is refused with a typed error BEFORE any
// embed call, and nothing is handed to the atomic inserter, even though an
// earlier item in the same batch is within the cap.
func TestBulkImport_OverCapContentRejected(t *testing.T) {
	fake := &fakeBulkInserter{}
	b := newBulkBrain(fake, staticEmbedder{})
	b.cfg = &config.Config{BulkImportMaxContentChars: 5}

	items := []BulkItem{
		{Content: "short", ThoughtType: "note"},
		{Content: "this content is too long for the cap", ThoughtType: "note"},
	}
	ids, err := b.BulkImport(context.Background(), items, "import")
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.True(t, errors.Is(err, ErrContentTooLong), "expected ErrContentTooLong, got %v", err)
	assert.False(t, fake.called, "no write may be attempted when any item exceeds the content-length cap")
}

// TestBulkImport_WithinCapsSucceeds asserts a batch at exactly the configured
// caps is accepted, proving the cap checks are inclusive boundaries, not
// off-by-one rejections.
func TestBulkImport_WithinCapsSucceeds(t *testing.T) {
	fake := &fakeBulkInserter{}
	b := newBulkBrain(fake, staticEmbedder{})
	b.cfg = &config.Config{BulkImportMaxItems: 2, BulkImportMaxContentChars: 5}

	items := []BulkItem{
		{Content: "one", ThoughtType: "note"},
		{Content: "two", ThoughtType: "note"},
	}
	ids, err := b.BulkImport(context.Background(), items, "import")
	require.NoError(t, err)
	assert.Len(t, ids, 2)
	assert.True(t, fake.called)
}

// TestBulkImport_DefaultCapsApplyWhenCfgNil asserts the package-level default
// caps are used when Brain has no config at all (cfg is nil), so a Brain
// constructed without a config (as several existing tests and callers do)
// still enforces a bound rather than accepting an unbounded batch.
func TestBulkImport_DefaultCapsApplyWhenCfgNil(t *testing.T) {
	assert.Equal(t, DefaultBulkImportMaxItems, effectiveBulkMaxItems(nil))
	assert.Equal(t, DefaultBulkImportMaxContentRunes, effectiveBulkMaxContentRunes(nil))
}

// TestCaptureExtracted_StoreFailureIsLoud asserts that when the atomic store of
// extracted candidates fails, DeepCapture returns a real error rather than a
// success-shaped string that embeds the word "failed". This pins the OB-032
// fail-loud contract for the extract-then-auto_capture path.
func TestCaptureExtracted_StoreFailureIsLoud(t *testing.T) {
	fake := &fakeBulkInserter{err: errors.New("injected store failure")}
	b := &Brain{embedder: staticEmbedder{}}
	b.bulkInsertFn = fake.insert
	b.captureFn = b.Capture
	b.extractFn = func(_ context.Context, _ string) ([]extract.Candidate, error) {
		return []extract.Candidate{
			{Content: "candidate one", ThoughtType: "note"},
			{Content: "candidate two", ThoughtType: "insight"},
		}, nil
	}

	parsed := intent.ParsedIntent{Intent: intent.Extract, Text: "long input", ThoughtType: "note"}
	result, err := b.DeepCapture(context.Background(), parsed, "cli")
	require.Error(t, err, "a store failure must surface as an error, not a success string")
	assert.Empty(t, result)
	assert.NotContains(t, strings.ToLower(result), "captured",
		"no success-shaped confirmation may be returned on store failure")
	assert.True(t, fake.called, "the atomic inserter must have been invoked for the candidate batch")
}

// TestCaptureExtracted_HappyPath asserts the exact original and extracted
// candidates are stored together in one atomic batch.
func TestCaptureExtracted_HappyPath(t *testing.T) {
	fake := &fakeBulkInserter{}
	b := &Brain{embedder: staticEmbedder{}}
	b.bulkInsertFn = fake.insert
	b.captureFn = b.Capture
	b.extractFn = func(_ context.Context, _ string) ([]extract.Candidate, error) {
		return []extract.Candidate{
			{Content: "candidate one", ThoughtType: "note"},
			{Content: "candidate two", ThoughtType: "insight"},
		}, nil
	}

	parsed := intent.ParsedIntent{Intent: intent.Extract, Text: "long input", ThoughtType: "note"}
	result, err := b.DeepCapture(context.Background(), parsed, "cli")
	require.NoError(t, err)
	assert.True(t, fake.called)
	require.Len(t, fake.got, 3, "the original and both candidates must go into one atomic batch")
	assert.Equal(t, "long input", fake.got[0].Content)
	assert.Equal(t, "original_source", fake.got[0].Metadata["capture_role"])
	assert.Equal(t, "candidate one", fake.got[1].Content)
	assert.Equal(t, "ai_extracted", fake.got[1].Metadata["capture_role"])
	assert.Equal(t, fake.got[0].Metadata["capture_id"], fake.got[1].Metadata["capture_id"])
	assert.Contains(t, result, "Stored the original note")
	assert.Contains(t, result, "2 additional thoughts")
}
