package converse

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/windingriverholdings/openbrain/internal/model"
)

// citationPattern matches a bracketed citation group: [1], [1, 2], [1,2,3].
//
// It deliberately requires digits only, so markdown links like [text](url) and
// prose brackets are not mistaken for citations.
var citationPattern = regexp.MustCompile(`\[\s*\d+(?:\s*,\s*\d+)*\s*\]`)

// digitPattern extracts every number inside a matched citation group. A group
// like [1, 2] contains two distinct citations and both must be recovered.
var digitPattern = regexp.MustCompile(`\d+`)

// ParseCitations returns the distinct 1-based note numbers cited in the answer,
// in ascending order.
func ParseCitations(answer string) []int {
	seen := make(map[int]bool)
	for _, group := range citationPattern.FindAllString(answer, -1) {
		for _, digits := range digitPattern.FindAllString(group, -1) {
			n, err := strconv.Atoi(digits)
			if err != nil || n <= 0 {
				continue
			}
			seen[n] = true
		}
	}
	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// VerifyCitations reconciles an answer's [n] markers with the notes actually
// supplied to the model.
//
// Two problems are corrected. First, models cite indices that do not exist
// (for example [11] when 8 notes were supplied); those are stripped, because a
// citation that cannot be resolved is worse than no citation. Second, models
// typically cite a subset of what was retrieved, so returning all retrieved
// notes as "sources" overstates what the answer is actually grounded in.
//
// The returned answer is renumbered so bracket numbers are contiguous from 1
// and index directly into the returned sources slice, which is what the UI
// needs to make citations clickable.
func VerifyCitations(answer string, notes []model.ThoughtRow) (string, []model.ThoughtRow) {
	cited := ParseCitations(answer)

	// Keep only citations that resolve to a supplied note.
	valid := make([]int, 0, len(cited))
	for _, n := range cited {
		if n >= 1 && n <= len(notes) {
			valid = append(valid, n)
		}
	}

	// No resolvable citations: the answer is ungrounded or the model declined
	// to cite. Return every retrieved note so the user can still audit what
	// was read, but leave the text untouched rather than inventing structure.
	if len(valid) == 0 {
		return stripUnresolvableCitations(answer, len(notes)), notes
	}

	// Build old -> new numbering over the cited notes only.
	renumber := make(map[int]int, len(valid))
	sources := make([]model.ThoughtRow, 0, len(valid))
	for _, old := range valid {
		sources = append(sources, notes[old-1])
		renumber[old] = len(sources)
	}

	rewritten := citationPattern.ReplaceAllStringFunc(answer, func(group string) string {
		mapped := make([]string, 0, 4)
		for _, digits := range digitPattern.FindAllString(group, -1) {
			n, err := strconv.Atoi(digits)
			if err != nil {
				continue
			}
			if to, ok := renumber[n]; ok {
				mapped = append(mapped, strconv.Itoa(to))
			}
		}
		if len(mapped) == 0 {
			// Every number in this group was unresolvable; drop the marker.
			return ""
		}
		return "[" + strings.Join(mapped, ", ") + "]"
	})

	return strings.TrimSpace(collapseSpaces(rewritten)), sources
}

// stripUnresolvableCitations removes bracket markers pointing past the end of
// the supplied notes, so the UI never renders a citation that cannot open.
func stripUnresolvableCitations(answer string, noteCount int) string {
	rewritten := citationPattern.ReplaceAllStringFunc(answer, func(group string) string {
		kept := make([]string, 0, 4)
		for _, digits := range digitPattern.FindAllString(group, -1) {
			n, err := strconv.Atoi(digits)
			if err != nil {
				continue
			}
			if n >= 1 && n <= noteCount {
				kept = append(kept, strconv.Itoa(n))
			}
		}
		if len(kept) == 0 {
			return ""
		}
		return "[" + strings.Join(kept, ", ") + "]"
	})
	return strings.TrimSpace(collapseSpaces(rewritten))
}

// collapseSpaces tidies the double spaces and orphaned punctuation spacing left
// behind when a citation marker is removed mid-sentence.
func collapseSpaces(s string) string {
	s = strings.ReplaceAll(s, "  ", " ")
	s = strings.ReplaceAll(s, " .", ".")
	s = strings.ReplaceAll(s, " ,", ",")
	return s
}
