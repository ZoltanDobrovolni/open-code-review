// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegate

import (
	"strings"
	"testing"
)

func testPreview() PreviewSpec {
	spec := PreviewSpec{
		SchemaVersion: "1",
		Mode:          "workspace",
		Repository:    ".",
		Files: []PreviewFile{
			{ID: "file-0001", Path: "main.go", Status: "modified", NewFileLines: 10, Reviewable: true},
			{ID: "file-0002", Path: "generated.go", Status: "modified", Reviewable: false, ExcludeReason: "default_path"},
		},
	}
	spec.SpecSHA256 = hashPreview(spec)
	return spec
}

func validResult(spec PreviewSpec) ReviewResult {
	return ReviewResult{
		SchemaVersion: "1",
		SpecSHA256:    spec.SpecSHA256,
		ReviewedFiles: []FileRef{{ID: "file-0001", Path: "main.go", Status: "modified"}},
		SkippedFiles:  []SkippedFile{},
		Findings: []Finding{{
			Path:      "main.go",
			StartLine: 4,
			EndLine:   5,
			Severity:  "high",
			Category:  "bug",
			Content:   "This can return an invalid result.",
		}},
		Coverage: Coverage{TotalFiles: 1, ReviewedFiles: 1, SkippedFiles: 0, CoverageRate: 1},
	}
}

func TestValidateReviewResultAcceptsCompleteResult(t *testing.T) {
	spec := testPreview()
	if err := ValidateReviewResult(validResult(spec), spec); err != nil {
		t.Fatalf("ValidateReviewResult() error = %v", err)
	}
}

func TestValidateReviewResultRejectsUnaccountedFile(t *testing.T) {
	spec := testPreview()
	result := validResult(spec)
	result.ReviewedFiles = nil
	result.Coverage = Coverage{}
	if err := ValidateReviewResult(result, spec); err == nil || !strings.Contains(err.Error(), "accounts for") {
		t.Fatalf("ValidateReviewResult() error = %v, want unaccounted-file error", err)
	}
}

func TestValidateReviewResultRejectsPreviewMismatch(t *testing.T) {
	spec := testPreview()
	result := validResult(spec)
	result.SpecSHA256 = "different"
	if err := ValidateReviewResult(result, spec); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("ValidateReviewResult() error = %v, want mismatch error", err)
	}
}

func TestValidateReviewResultRequiresFileIDForDuplicatePaths(t *testing.T) {
	spec := testPreview()
	spec.Files = append(spec.Files, PreviewFile{
		ID:         "file-0003",
		Path:       "main.go",
		Status:     "renamed",
		Reviewable: true,
	})
	spec.SpecSHA256 = hashPreview(spec)
	result := validResult(spec)
	result.ReviewedFiles = append(result.ReviewedFiles, FileRef{ID: "file-0003", Path: "main.go", Status: "renamed"})
	result.Coverage = Coverage{TotalFiles: 2, ReviewedFiles: 2, CoverageRate: 1}
	result.Findings = append(result.Findings, Finding{Path: "main.go", Content: "Ambiguous finding."})
	if err := ValidateReviewResult(result, spec); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ValidateReviewResult() error = %v, want ambiguity error", err)
	}
}

func TestDecodeReviewResultRejectsUnknownFields(t *testing.T) {
	_, err := DecodeReviewResult(strings.NewReader(`{"schema_version":"1","unexpected":true}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("DecodeReviewResult() error = %v, want unknown-field error", err)
	}
}
