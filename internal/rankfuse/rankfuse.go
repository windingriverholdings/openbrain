// Package rankfuse combines independently ranked search results without
// comparing scores from different retrieval systems.
package rankfuse

import (
	"sort"

	"github.com/windingriverholdings/openbrain/internal/model"
)

// DefaultK is the standard reciprocal-rank-fusion damping constant. Larger
// values flatten the contribution curve, so lower-ranked hits still matter;
// smaller values concentrate weight on the very top of each list.
const DefaultK = 60

// FuseRRF combines independently ranked result lists using reciprocal rank
// fusion:
//
//	score(row) = sum over lists of 1 / (k + rank)
//
// Only positions are read, never the source scores, so lists produced by
// systems with incomparable score scales (for example full-text rank versus
// cosine similarity) contribute on equal terms. A row ranked highly by several
// independent lists outranks a row ranked highly by only one, which is the
// agreement signal this fusion exists to capture.
//
// The returned rows carry the fused score in Score, replacing whatever
// per-axis score they arrived with, since those values are not comparable
// across lists.
func FuseRRF(k int, lists ...[]model.ThoughtRow) []model.ThoughtRow {
	if k < 1 {
		k = DefaultK
	}
	type fused struct {
		row   model.ThoughtRow
		score float64
	}
	byID := make(map[string]fused)
	for _, list := range lists {
		seen := make(map[string]bool)
		for rank, row := range list {
			if row.ID == "" || seen[row.ID] {
				continue
			}
			seen[row.ID] = true
			item := byID[row.ID]
			if item.row.ID == "" {
				item.row = row
			}
			item.score += 1.0 / float64(k+rank+1)
			byID[row.ID] = item
		}
	}
	results := make([]model.ThoughtRow, 0, len(byID))
	for _, item := range byID {
		score := item.score
		item.row.Score = &score
		results = append(results, item.row)
	}
	sort.SliceStable(results, func(i, j int) bool {
		left, right := score(results[i]), score(results[j])
		if left == right {
			return results[i].ID < results[j].ID
		}
		return left > right
	})
	return results
}

func score(row model.ThoughtRow) float64 {
	if row.Score == nil {
		return 0
	}
	return *row.Score
}
