package converse

import (
	"reflect"
	"strings"
	"testing"

	"github.com/windingriverholdings/openbrain/internal/model"
)

func rows(ids ...string) []model.ThoughtRow {
	out := make([]model.ThoughtRow, len(ids))
	for i, id := range ids {
		out[i] = model.ThoughtRow{ID: id, Content: "content " + id}
	}
	return out
}

func TestParseCitations(t *testing.T) {
	cases := []struct {
		name, answer string
		want         []int
	}{
		{"single", "We chose X [1].", []int{1}},
		{"group", "We chose X [1, 2].", []int{1, 2}},
		{"group no space", "We chose X [1,2,3].", []int{1, 2, 3}},
		{"multiple markers", "A [1] and B [3].", []int{1, 3}},
		{"deduplicated", "A [1] and B [1].", []int{1}},
		{"sorted", "A [3] then B [1].", []int{1, 3}},
		{"none", "No citations here.", nil},
		{"markdown link ignored", "See [docs](https://example.com).", nil},
		{"prose bracket ignored", "The [important] thing.", nil},
		{"padded", "A [ 1 , 2 ].", []int{1, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCitations(tc.answer)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseCitations(%q) = %v, want %v", tc.answer, got, tc.want)
			}
		})
	}
}

// TestVerifyCitations_NarrowsToCitedSources is the behaviour that makes the
// sources drawer honest: returning every retrieved note overstates what the
// answer is actually grounded in.
func TestVerifyCitations_NarrowsToCitedSources(t *testing.T) {
	notes := rows("a", "b", "c", "d")
	answer, sources := VerifyCitations("We chose X [2] but see also [4].", notes)

	if len(sources) != 2 {
		t.Fatalf("got %d sources, want 2", len(sources))
	}
	if sources[0].ID != "b" || sources[1].ID != "d" {
		t.Errorf("sources = %s,%s want b,d", sources[0].ID, sources[1].ID)
	}
	// Renumbered so [n] indexes directly into the returned slice.
	if answer != "We chose X [1] but see also [2]." {
		t.Errorf("answer = %q", answer)
	}
}

func TestVerifyCitations_RenumberingMatchesSourceOrder(t *testing.T) {
	notes := rows("a", "b", "c", "d", "e")
	answer, sources := VerifyCitations("First [5] then [3].", notes)

	nums := ParseCitations(answer)
	for _, n := range nums {
		if n < 1 || n > len(sources) {
			t.Fatalf("citation [%d] out of range for %d sources", n, len(sources))
		}
	}
	// Sources follow ascending original order, so [3]->1 (c) and [5]->2 (e).
	if sources[0].ID != "c" || sources[1].ID != "e" {
		t.Errorf("sources = %s,%s want c,e", sources[0].ID, sources[1].ID)
	}
	if answer != "First [2] then [1]." {
		t.Errorf("answer = %q", answer)
	}
}

// TestVerifyCitations_DropsHallucinatedIndices covers models citing a note
// number that was never supplied. An unresolvable citation renders as a control
// that cannot open anything, so it is worse than no citation.
func TestVerifyCitations_DropsHallucinatedIndices(t *testing.T) {
	notes := rows("a", "b")
	answer, sources := VerifyCitations("We chose X [1] and Y [7].", notes)

	if strings.Contains(answer, "[7]") {
		t.Errorf("answer %q retains out-of-range citation", answer)
	}
	if len(sources) != 1 || sources[0].ID != "a" {
		t.Errorf("sources = %v, want just a", sources)
	}
}

func TestVerifyCitations_PartiallyValidGroup(t *testing.T) {
	notes := rows("a", "b")
	answer, sources := VerifyCitations("Both [1, 9] agree.", notes)

	if strings.Contains(answer, "9") {
		t.Errorf("answer %q retains out-of-range citation", answer)
	}
	if len(sources) != 1 {
		t.Fatalf("got %d sources, want 1", len(sources))
	}
	if !strings.Contains(answer, "[1]") {
		t.Errorf("answer %q lost the valid citation", answer)
	}
}

// TestVerifyCitations_UncitedAnswerKeepsAllSources: when the model declines to
// cite, the user must still be able to audit what was read.
func TestVerifyCitations_UncitedAnswerKeepsAllSources(t *testing.T) {
	notes := rows("a", "b", "c")
	answer, sources := VerifyCitations("The notes do not cover that.", notes)

	if len(sources) != 3 {
		t.Errorf("got %d sources, want all 3 retained", len(sources))
	}
	if answer != "The notes do not cover that." {
		t.Errorf("answer was altered: %q", answer)
	}
}

func TestVerifyCitations_AllCitationsHallucinated(t *testing.T) {
	notes := rows("a")
	answer, sources := VerifyCitations("Per [4] and [5] we shipped.", notes)

	if ParseCitations(answer) != nil && len(ParseCitations(answer)) != 0 {
		t.Errorf("answer %q should have no resolvable citations left", answer)
	}
	if len(sources) != 1 {
		t.Errorf("got %d sources, want the retrieved note retained for audit", len(sources))
	}
}

func TestVerifyCitations_NoNotes(t *testing.T) {
	answer, sources := VerifyCitations("Nothing found.", nil)
	if answer != "Nothing found." {
		t.Errorf("answer = %q", answer)
	}
	if len(sources) != 0 {
		t.Errorf("got %d sources, want 0", len(sources))
	}
}

func TestVerifyCitations_PreservesMarkdownLinks(t *testing.T) {
	notes := rows("a")
	answer, _ := VerifyCitations("See [docs](https://example.com) and [1].", notes)
	if !strings.Contains(answer, "[docs](https://example.com)") {
		t.Errorf("markdown link mangled: %q", answer)
	}
}
