// Package intent classifies natural language messages into OpenBrain actions
// using regex pattern matching — no LLM required.
package intent

import (
	"regexp"
	"strings"
)

// Intent represents a classified user action.
type Intent string

const (
	Capture   Intent = "capture"
	Search    Intent = "search"
	Review    Intent = "review"
	Stats     Intent = "stats"
	Help      Intent = "help"
	Supersede Intent = "supersede"
	Extract   Intent = "extract"
	Reload    Intent = "reload"
)

// ParsedIntent holds the classified intent and extracted content.
type ParsedIntent struct {
	Intent         Intent
	Text           string
	ThoughtType    string
	Tags           []string
	SupersedeQuery *string
}

const DeepCaptureThreshold = 200

var (
	searchPatterns = regexp.MustCompile(
		`(?i)^(search|find|look up|what do i know about|recall|remember|remind me|` +
			`have i (thought|noted|written) about|show me|retrieve|query)[:\s]+`)

	capturePatterns = regexp.MustCompile(
		`(?i)^(remember|save|capture|note|log|store|record|add|write down|` +
			`decided?|insight:|learning:|realised?|met |meeting with)[:\s]*`)

	decisionHint = regexp.MustCompile(
		`(?i)\b(decided?|chose|choice|picked|going with|will use|won't use|rejected)\b`)

	insightHint = regexp.MustCompile(
		`(?i)\b(realised?|learned?|noticed|insight|pattern|key (takeaway|learning))\b`)

	personHint = regexp.MustCompile(
		`(?i)\b(met |talked? (to|with)|called?|spoke (to|with)|email(ed)?)\b`)

	meetingHint = regexp.MustCompile(
		`(?i)\b(meeting|standup|call|sync|retrospective|1:1|one.on.one)\b`)

	reviewPatterns = regexp.MustCompile(
		`(?i)(weekly review|week review|review( the)? week|what happened this week` +
			`|this week|past week|last 7 days|summarise( the)? week` +
			`|give me a (weekly |week )?review|show me (the )?(weekly |week )?review)`)

	statsPatterns = regexp.MustCompile(
		`(?i)(^stats?$|statistics|how many (thoughts|memories|notes)|` +
			`brain stats|knowledge base stats|^count$` +
			`|send me (some |the |my )?(stats?|status)|give me (some |the |my )?(stats?|status)` +
			`|show me (some |the |my )?(stats?|status)|what (are|is) (my |the )?(stats?|status))`)

	supersedePatterns = regexp.MustCompile(
		`(?i)^(actually[,:]?\s*|update:\s*|correction:\s*|changed?:\s*|no longer[,:]?\s*|now instead[,:]?\s*)`)

	extractPrefix = regexp.MustCompile(`(?i)^extract:\s*`)

	helpPatterns = regexp.MustCompile(
		`(?i)(^help$|^commands$|^what can you do|^\?+$|^how (do|does) (this|it) work)`)
)

// InferType guesses the thought type from content text.
func InferType(text string) string {
	if decisionHint.MatchString(text) {
		return "decision"
	}
	if insightHint.MatchString(text) {
		return "insight"
	}
	if personHint.MatchString(text) {
		return "person"
	}
	if meetingHint.MatchString(text) {
		return "meeting"
	}
	return "note"
}

// Parse classifies a natural language message into a structured intent.
func Parse(message string) ParsedIntent {
	msg := strings.TrimSpace(message)

	if helpPatterns.MatchString(msg) {
		return ParsedIntent{Intent: Help, Text: msg, ThoughtType: "note"}
	}

	if statsPatterns.MatchString(msg) {
		return ParsedIntent{Intent: Stats, Text: msg, ThoughtType: "note"}
	}

	if reviewPatterns.MatchString(msg) {
		return ParsedIntent{Intent: Review, Text: msg, ThoughtType: "note"}
	}

	// Extract prefix: explicit deep capture
	if loc := extractPrefix.FindStringIndex(msg); loc != nil {
		content := strings.TrimSpace(msg[loc[1]:])
		return ParsedIntent{Intent: Extract, Text: content, ThoughtType: "note"}
	}

	// Supersede: "actually,...", "update:...", "correction:..."
	if loc := supersedePatterns.FindStringIndex(msg); loc != nil {
		content := strings.TrimSpace(msg[loc[1]:])
		q := content
		return ParsedIntent{
			Intent:         Supersede,
			Text:           content,
			ThoughtType:    InferType(content),
			SupersedeQuery: &q,
		}
	}

	if loc := searchPatterns.FindStringIndex(msg); loc != nil {
		query := strings.TrimSpace(msg[loc[1]:])
		return ParsedIntent{Intent: Search, Text: query, ThoughtType: "note"}
	}

	if loc := capturePatterns.FindStringIndex(msg); loc != nil {
		content := strings.TrimSpace(msg[loc[1]:])
		if content == "" {
			content = msg
		}
		return ParsedIntent{Intent: Capture, Text: content, ThoughtType: InferType(content)}
	}

	// Default: questions -> search, long text -> extract, else capture
	lower := strings.ToLower(msg)
	if strings.HasSuffix(msg, "?") ||
		strings.HasPrefix(lower, "what") ||
		strings.HasPrefix(lower, "who") ||
		strings.HasPrefix(lower, "when") ||
		strings.HasPrefix(lower, "where") ||
		strings.HasPrefix(lower, "how") ||
		strings.HasPrefix(lower, "why") {
		return ParsedIntent{Intent: Search, Text: strings.TrimRight(msg, "?"), ThoughtType: "note"}
	}

	if len(msg) > DeepCaptureThreshold {
		return ParsedIntent{Intent: Extract, Text: msg, ThoughtType: "note"}
	}

	return ParsedIntent{Intent: Capture, Text: msg, ThoughtType: InferType(msg)}
}
