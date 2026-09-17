// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegate

import (
	"strings"
	"testing"
)

func TestCountFileLines(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int
	}{
		{name: "empty", content: "", want: 0},
		{name: "one line with newline", content: "one\n", want: 1},
		{name: "one line without newline", content: "one", want: 1},
		{name: "two lines", content: "one\ntwo\n", want: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countFileLines(tc.content); got != tc.want {
				t.Errorf("countFileLines() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestValidatePreviewRejectsUnsafeFilePath(t *testing.T) {
	spec := PreviewSpec{
		SchemaVersion: "1",
		Repository:    ".",
		Files: []PreviewFile{{
			ID:         "file-0001",
			Path:       "../outside.go",
			Status:     "modified",
			Reviewable: true,
		}},
	}
	spec.SpecSHA256 = hashPreview(spec)
	if err := ValidatePreview(spec); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("ValidatePreview() error = %v, want path error", err)
	}
}

func TestValidateReviewResultRejectsOutOfBoundsLine(t *testing.T) {
	spec := testPreview()
	result := validResult(spec)
	result.Findings[0].StartLine = 11
	result.Findings[0].EndLine = 11
	if err := ValidateReviewResult(result, spec); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("ValidateReviewResult() error = %v, want line-bound error", err)
	}
}
