// Package model defines shared domain types used across the OpenBrain system.
package model

import "time"

// ThoughtRow represents a thought record from the database.
type ThoughtRow struct {
	ID           string
	Content      string
	Summary      *string
	ThoughtType  string
	Tags         []string
	Source       string
	Metadata     map[string]any
	CreatedAt    time.Time
	UpdatedAt    time.Time
	IsCurrent    bool
	ValidFrom    *time.Time
	ValidUntil   *time.Time
	SupersededBy *string
	Score        *float64 // populated by search queries
}

// SubjectLink represents a subject to link to a thought.
type SubjectLink struct {
	Name string
	Type string // "person", "tool", "concept", etc.
}

// Stats holds aggregate brain statistics.
type Stats struct {
	Total    int
	ThisWeek int
	Today    int
	OldestAt *time.Time
	NewestAt *time.Time
	ByType   map[string]int
}
