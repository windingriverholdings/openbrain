package intent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type intentTestCase struct {
	Input                string  `json:"input"`
	ExpectedIntent       string  `json:"expected_intent"`
	ExpectedText         string  `json:"expected_text"`
	ExpectedThoughtType  string  `json:"expected_thought_type"`
	ExpectedTags         []string `json:"expected_tags,omitempty"`
	ExpectedSupersedeQ   *string `json:"expected_supersede_query,omitempty"`
}

func testdataPath(name string) string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "testdata", name)
}

func loadIntentFixtures(t *testing.T) []intentTestCase {
	t.Helper()
	data, err := os.ReadFile(testdataPath("intent_cases.json"))
	require.NoError(t, err, "failed to read intent_cases.json")

	var cases []intentTestCase
	require.NoError(t, json.Unmarshal(data, &cases))
	return cases
}

func TestParse(t *testing.T) {
	cases := loadIntentFixtures(t)
	for _, tc := range cases {
		t.Run(tc.Input, func(t *testing.T) {
			got := Parse(tc.Input)
			assert.Equal(t, tc.ExpectedIntent, string(got.Intent), "intent mismatch")
			assert.Equal(t, tc.ExpectedText, got.Text, "text mismatch")
			assert.Equal(t, tc.ExpectedThoughtType, got.ThoughtType, "thought_type mismatch")

			if tc.ExpectedSupersedeQ != nil {
				require.NotNil(t, got.SupersedeQuery, "expected supersede_query")
				assert.Equal(t, *tc.ExpectedSupersedeQ, *got.SupersedeQuery)
			} else {
				assert.Nil(t, got.SupersedeQuery, "unexpected supersede_query")
			}
		})
	}
}

type inferTypeTestCase struct {
	Input        string `json:"input"`
	ExpectedType string `json:"expected_type"`
	ActualType   string `json:"actual_type"`
}

func TestInferType(t *testing.T) {
	data, err := os.ReadFile(testdataPath("infer_type_cases.json"))
	require.NoError(t, err)

	var cases []inferTypeTestCase
	require.NoError(t, json.Unmarshal(data, &cases))

	for _, tc := range cases {
		t.Run(tc.Input, func(t *testing.T) {
			got := InferType(tc.Input)
			// Match the actual Python behavior (not the expected — some have known bugs)
			assert.Equal(t, tc.ActualType, got, "infer_type mismatch")
		})
	}
}
