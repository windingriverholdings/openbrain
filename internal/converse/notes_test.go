package converse

import (
	"strings"
	"testing"
	"time"

	"github.com/windingriverholdings/openbrain/internal/model"
)

func notesOfLength(lengths ...int) []model.ThoughtRow {
	rows := make([]model.ThoughtRow, len(lengths))
	for i, n := range lengths {
		rows[i] = model.ThoughtRow{
			ID:      string(rune('a' + i)),
			Content: strings.Repeat("x", n),
		}
	}
	return rows
}

// TestFormatNotes_RespectsAggregateBudget is the core regression test. The old
// implementation capped each note at 4000 runes with no aggregate limit, so ten
// notes produced a ~40k-rune block that overflowed the model's context window.
func TestFormatNotes_RespectsAggregateBudget(t *testing.T) {
	notes := notesOfLength(8000, 8000, 8000, 8000, 8000, 8000, 8000, 8000)
	const maxTokens = 1000

	got := FormatNotes(notes, maxTokens)

	limit := int(float64(maxTokens) * runesPerToken)
	if n := len([]rune(got)); n > limit {
		t.Errorf("block is %d runes, exceeds budget of %d", n, limit)
	}
}

// TestFormatNotes_FairShare verifies a long leading note cannot starve later
// notes. Sequential spend-until-empty budgeting would drop the tail entirely,
// biasing the answer toward whatever retrieval happened to rank first.
func TestFormatNotes_FairShare(t *testing.T) {
	notes := notesOfLength(20000, 100, 100, 20000)
	got := FormatNotes(notes, 1000)

	for i := 1; i <= len(notes); i++ {
		marker := "[" + string(rune('0'+i)) + "]"
		if !strings.Contains(got, marker) {
			t.Errorf("note %d missing from block; long notes starved the tail", i)
		}
	}
	// The two short notes must survive intact.
	if strings.Count(got, strings.Repeat("x", 100)) < 2 {
		t.Error("short notes were truncated despite ample budget")
	}
}

func TestFormatNotes_RedistributesUnusedBudget(t *testing.T) {
	// Three tiny notes and one huge one: the huge note should receive far more
	// than a flat one-quarter share.
	notes := notesOfLength(10, 10, 10, 20000)
	got := FormatNotes(notes, 1000)

	flatShare := int(float64(1000)*runesPerToken) / 4
	longRun := 0
	for _, line := range strings.Split(got, "\n") {
		if c := strings.Count(line, "x"); c > longRun {
			longRun = c
		}
	}
	if longRun <= flatShare {
		t.Errorf("long note got %d runes, expected more than the flat share %d", longRun, flatShare)
	}
}

func TestFormatNotes_NumbersSequentiallyFromOne(t *testing.T) {
	got := FormatNotes(notesOfLength(50, 50, 50), 1000)
	for _, want := range []string{"[1]", "[2]", "[3]"} {
		if !strings.Contains(got, want) {
			t.Errorf("block missing marker %s", want)
		}
	}
	if strings.Contains(got, "[0]") {
		t.Error("numbering must start at 1 to match the answer prompt")
	}
}

func TestFormatNotes_IncludesMetadata(t *testing.T) {
	notes := []model.ThoughtRow{{
		ID:          "id-1",
		Content:     "we chose pgvector",
		ThoughtType: "decision",
		Tags:        []string{"db", "search"},
		CreatedAt:   time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
	}}
	got := FormatNotes(notes, 1000)

	for _, want := range []string{"decision", "2026-07-02", "db, search"} {
		if !strings.Contains(got, want) {
			t.Errorf("block missing metadata %q; recency and type change interpretation", want)
		}
	}
	// The UUID wastes prompt tokens and is never useful to the model.
	if strings.Contains(got, "id-1") {
		t.Error("note UUID should not be sent in the prompt")
	}
}

func TestFormatNotes_Empty(t *testing.T) {
	if got := FormatNotes(nil, 1000); got != "" {
		t.Errorf("FormatNotes(nil) = %q, want empty", got)
	}
}

func TestFormatNotes_DegenerateBudgetStillNumbersNotes(t *testing.T) {
	// Even with an unusable budget, markers must exist so any citation the
	// model emits can still be resolved.
	got := FormatNotes(notesOfLength(5000, 5000, 5000), 1)
	for _, want := range []string{"[1]", "[2]", "[3]"} {
		if !strings.Contains(got, want) {
			t.Errorf("degenerate budget dropped marker %s", want)
		}
	}
}

func TestTruncateRunes_EllipsisInsideBudget(t *testing.T) {
	// The old code appended the ellipsis after slicing to the cap, overrunning
	// the budget by one rune per note.
	got := truncateRunes(strings.Repeat("x", 100), 10)
	if n := len([]rune(got)); n != 10 {
		t.Errorf("len = %d, want exactly 10", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("got %q, want a trailing ellipsis", got)
	}
}

func TestTruncateRunes_ShortInputUnchanged(t *testing.T) {
	if got := truncateRunes("hello", 10); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

func TestTruncateRunes_UnicodeSafe(t *testing.T) {
	got := truncateRunes(strings.Repeat("héllo→", 50), 12)
	if n := len([]rune(got)); n != 12 {
		t.Errorf("len = %d runes, want 12", n)
	}
	if !strings.ContainsRune(got, '…') {
		t.Error("expected ellipsis")
	}
}
