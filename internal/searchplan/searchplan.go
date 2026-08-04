// Package searchplan defines the constrained, read-only plan used by AI Search.
package searchplan

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	MaxSteps = 3
	MaxTopK  = 25
)

// Plan is the only structured output accepted from the AI Search planner.
type Plan struct {
	Steps []Step `json:"steps"`
}

// Step describes one allowlisted search operation. It cannot contain SQL or a
// database operation name, so execution remains under Brain's control.
type Step struct {
	Query          string   `json:"query"`
	Mode           string   `json:"mode"`
	ThoughtType    string   `json:"thought_type,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	IncludeHistory bool     `json:"include_history,omitempty"`
	TopK           int      `json:"top_k,omitempty"`
}

// Parse parses strict planner JSON, tolerating a surrounding markdown fence.
func Parse(raw string) (Plan, error) {
	text := strings.TrimSpace(raw)
	if strings.HasPrefix(text, "```") {
		lines := make([]string, 0)
		for _, line := range strings.Split(text, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "```") {
				lines = append(lines, line)
			}
		}
		text = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	var plan Plan
	if err := json.Unmarshal([]byte(text), &plan); err != nil {
		return Plan{}, fmt.Errorf("invalid search plan JSON: %w", err)
	}
	if len(plan.Steps) == 0 || len(plan.Steps) > MaxSteps {
		return Plan{}, fmt.Errorf("search plan must contain 1-%d steps", MaxSteps)
	}
	for i := range plan.Steps {
		// Small local models sometimes invent a label such as "classification"
		// even though it is not a stored thought type. Ignore that optional
		// filter rather than failing an otherwise safe search plan.
		if !validThoughtType(plan.Steps[i].ThoughtType) {
			plan.Steps[i].ThoughtType = ""
		}
		if err := ValidateStep(&plan.Steps[i]); err != nil {
			return Plan{}, fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	return plan, nil
}

func validThoughtType(value string) bool {
	switch value {
	case "", "decision", "insight", "person", "meeting", "idea", "note", "memory":
		return true
	default:
		return false
	}
}

func ValidateStep(step *Step) error {
	if strings.TrimSpace(step.Query) == "" {
		return fmt.Errorf("query is required")
	}
	switch step.Mode {
	case "vector", "keyword", "hybrid":
	default:
		return fmt.Errorf("mode %q is not allowed", step.Mode)
	}
	if !validThoughtType(step.ThoughtType) {
		return fmt.Errorf("thought_type %q is not allowed", step.ThoughtType)
	}
	if step.TopK <= 0 {
		step.TopK = 10
	}
	if step.TopK > MaxTopK {
		return fmt.Errorf("top_k cannot exceed %d", MaxTopK)
	}
	if len(step.Tags) > 20 {
		return fmt.Errorf("too many tags")
	}
	return nil
}
