package extract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type extractTestCase struct {
	Name            string     `json:"name"`
	RawInput        string     `json:"raw_input"`
	ExpectedCount   int        `json:"expected_count"`
	ExpectedResults []expected `json:"expected_results"`
}

type expected struct {
	Content         string   `json:"content"`
	ThoughtType     string   `json:"thought_type"`
	Tags            []string `json:"tags"`
	Subjects        []string `json:"subjects"`
	SupersedesQuery *string  `json:"supersedes_query"`
}

func testdataPath(name string) string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "testdata", name)
}

func TestParseExtractionResponse(t *testing.T) {
	data, err := os.ReadFile(testdataPath("extract_parse_cases.json"))
	require.NoError(t, err)

	var cases []extractTestCase
	require.NoError(t, json.Unmarshal(data, &cases))

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			got := ParseExtractionResponse(tc.RawInput)
			assert.Equal(t, tc.ExpectedCount, len(got), "count mismatch")

			for i, exp := range tc.ExpectedResults {
				if i >= len(got) {
					break
				}
				assert.Equal(t, exp.Content, got[i].Content, "content mismatch at %d", i)
				assert.Equal(t, exp.ThoughtType, got[i].ThoughtType, "thought_type mismatch at %d", i)

				// Compare slices nil-safely (Python [] == Go nil for empty)
				assertStringSliceEqual(t, exp.Tags, got[i].Tags, "tags at %d", i)
				assertStringSliceEqual(t, exp.Subjects, got[i].Subjects, "subjects at %d", i)

				if exp.SupersedesQuery != nil {
					require.NotNil(t, got[i].SupersedesQuery, "expected supersedes_query at %d", i)
					assert.Equal(t, *exp.SupersedesQuery, *got[i].SupersedesQuery)
				} else {
					assert.Nil(t, got[i].SupersedesQuery, "unexpected supersedes_query at %d", i)
				}
			}
		})
	}
}

// assertStringSliceEqual compares two string slices, treating nil and empty as equal.
func assertStringSliceEqual(t *testing.T, expected, actual []string, msgAndArgs ...any) {
	t.Helper()
	if len(expected) == 0 && len(actual) == 0 {
		return // both empty or nil — equivalent
	}
	assert.Equal(t, expected, actual, msgAndArgs...)
}
