package watcher

import (
	"path/filepath"
	"regexp"
	"strings"
)

// nonAlphanumHyphen matches any character that is not alphanumeric or a hyphen.
var nonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9-]`)

// collapseHyphens matches sequences of multiple hyphens.
var collapseHyphens = regexp.MustCompile(`-{2,}`)

// FolderTags extracts parent directory names relative to watchRoot and
// normalises them into tag-safe strings: lowercase, spaces to hyphens,
// non-alphanumeric characters stripped, deduplicated.
//
// A root-level file (no parent dirs between filePath and watchRoot)
// returns an empty slice.
func FolderTags(filePath string, watchRoot string) []string {
	cleanFile := filepath.Clean(filePath)
	cleanRoot := filepath.Clean(watchRoot)

	rel, err := filepath.Rel(cleanRoot, cleanFile)
	if err != nil {
		return nil
	}

	dir := filepath.Dir(rel)
	if dir == "." {
		return []string{}
	}

	parts := strings.Split(dir, string(filepath.Separator))
	seen := make(map[string]bool, len(parts))
	tags := make([]string, 0, len(parts))

	for _, p := range parts {
		tag := normalizeTag(p)
		if tag == "" {
			continue
		}
		if seen[tag] {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
	}

	return tags
}

// normalizeTag converts a directory name into a tag-safe string.
func normalizeTag(name string) string {
	lower := strings.ToLower(name)
	spaced := strings.ReplaceAll(lower, " ", "-")
	stripped := nonAlphanumHyphen.ReplaceAllString(spaced, "")
	collapsed := collapseHyphens.ReplaceAllString(stripped, "-")
	collapsed = strings.Trim(collapsed, "-")
	return collapsed
}
