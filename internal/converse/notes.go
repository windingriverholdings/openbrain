package converse

import (
	"fmt"
	"strings"

	"github.com/windingriverholdings/openbrain/internal/model"
)

// FormatNotes renders notes as a numbered block that fits a token budget.
//
// The budget is enforced in aggregate, not per note. A per-note cap alone
// (the previous behaviour) let 10 notes at 4000 runes each produce a
// 40,000-rune block: roughly 11k tokens, which both dominated latency and
// overflowed the context window. Overflow is silent on the server side and
// drops the *leading* notes, so it corrupts the [n] numbering the answer
// prompt depends on rather than merely losing detail.
//
// Budget is divided fairly: each note gets an equal share, and unused share
// from short notes is redistributed to longer ones. A purely sequential
// spend-until-empty approach would let note 1 consume the entire budget and
// starve note 8, biasing the answer toward whatever retrieval ranked first.
//
// Note metadata (type, tags, date) is included because it materially changes
// interpretation: "what did I decide" should weight a `decision` from last week
// over a `note` from two months ago, and the model cannot infer either from
// content alone.
func FormatNotes(notes []model.ThoughtRow, maxTokens int) string {
	if len(notes) == 0 {
		return ""
	}
	if maxTokens <= 0 {
		maxTokens = defaultPromptTokens
	}

	budget := int(float64(maxTokens) * runesPerToken)

	// Reserve room for the per-note header lines and separators before dividing
	// content budget, so the framing cannot push the block over the limit.
	// Each note emits: header + "\n" + content + "\n\n" => 3 separator runes.
	const separatorRunesPerNote = 3
	headers := make([]string, len(notes))
	overhead := 0
	for i, note := range notes {
		headers[i] = formatNoteHeader(i+1, note)
		overhead += len([]rune(headers[i])) + separatorRunesPerNote
	}

	contentBudget := budget - overhead
	if contentBudget < len(notes) {
		// Degenerate budget: emit headers only rather than nothing, so
		// citation numbering still resolves.
		var b strings.Builder
		for i := range notes {
			b.WriteString(headers[i])
			b.WriteString("\n")
		}
		return b.String()
	}

	allowances := allocateBudget(notes, contentBudget)

	var b strings.Builder
	for i, note := range notes {
		content := truncateRunes(strings.TrimSpace(note.Content), allowances[i])
		b.WriteString(headers[i])
		b.WriteString("\n")
		b.WriteString(content)
		b.WriteString("\n\n")
	}
	return b.String()
}

// allocateBudget distributes contentBudget across notes using progressive
// fair-sharing: notes shorter than an equal share release the remainder, which
// is redistributed among the notes that are still over their share. This
// converges in at most len(notes) passes and never exceeds the total budget.
func allocateBudget(notes []model.ThoughtRow, contentBudget int) []int {
	lengths := make([]int, len(notes))
	for i, note := range notes {
		lengths[i] = len([]rune(strings.TrimSpace(note.Content)))
	}

	allowances := make([]int, len(notes))
	settled := make([]bool, len(notes))
	remaining := contentBudget
	unsettled := len(notes)

	for unsettled > 0 {
		share := remaining / unsettled
		if share <= 0 {
			break
		}
		progressed := false
		for i := range notes {
			if settled[i] || lengths[i] > share {
				continue
			}
			// Note fits within its share; take only what it needs and
			// return the rest to the pool.
			allowances[i] = lengths[i]
			settled[i] = true
			remaining -= lengths[i]
			unsettled--
			progressed = true
		}
		if !progressed {
			// Every remaining note exceeds the share: split evenly and stop.
			for i := range notes {
				if !settled[i] {
					allowances[i] = share
				}
			}
			break
		}
	}
	return allowances
}

func formatNoteHeader(n int, note model.ThoughtRow) string {
	// The note UUID is deliberately omitted from the prompt: it costs ~10
	// tokens per note, is never useful to the model, and the caller already
	// maps bracket numbers back to IDs positionally.
	var parts []string
	if note.ThoughtType != "" {
		parts = append(parts, note.ThoughtType)
	}
	if !note.CreatedAt.IsZero() {
		parts = append(parts, note.CreatedAt.Format("2006-01-02"))
	}
	if len(note.Tags) > 0 {
		parts = append(parts, "tags: "+strings.Join(note.Tags, ", "))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("[%d]", n)
	}
	return fmt.Sprintf("[%d] (%s)", n, strings.Join(parts, "; "))
}

// truncateRunes shortens s to at most maxRunes *including* the ellipsis. The
// previous implementation appended the ellipsis after slicing to the cap,
// which overran the budget by one rune per note.
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}
