// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegate

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

const ResultSchemaVersion = 1

// ReviewResult is the strict host-agent output accepted by the local CLI.
type ReviewResult struct {
	SchemaVersion string        `json:"schema_version"`
	SpecSHA256    string        `json:"spec_sha256"`
	ReviewedFiles []FileRef     `json:"reviewed_files"`
	SkippedFiles  []SkippedFile `json:"skipped_files"`
	Findings      []Finding     `json:"findings"`
	Coverage      Coverage      `json:"coverage"`
}

// FileRef identifies one preview entry, including duplicate paths.
type FileRef struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Status string `json:"status"`
}

// SkippedFile records why a selected file was not reviewed.
type SkippedFile struct {
	FileRef
	Reason string `json:"reason"`
}

// Finding is a single host-agent review comment.
type Finding struct {
	ID        string `json:"id,omitempty"`
	FileID    string `json:"file_id,omitempty"`
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Category  string `json:"category,omitempty"`
	Content   string `json:"content"`
}

// Coverage is redundant by design: the validator checks it against the file
// identities so downstream consumers can display a trustworthy summary.
type Coverage struct {
	TotalFiles    int     `json:"total_files"`
	ReviewedFiles int     `json:"reviewed_files"`
	SkippedFiles  int     `json:"skipped_files"`
	CoverageRate  float64 `json:"coverage_rate"`
}

var allowedSeverities = map[string]bool{
	"critical": true,
	"high":     true,
	"medium":   true,
	"low":      true,
}

var allowedCategories = map[string]bool{
	"bug":             true,
	"security":        true,
	"performance":     true,
	"maintainability": true,
	"test":            true,
	"style":           true,
	"documentation":   true,
	"other":           true,
}

// DecodePreview decodes and verifies a preview JSON document.
func DecodePreview(r io.Reader) (PreviewSpec, error) {
	var spec PreviewSpec
	if err := decodeStrict(r, &spec); err != nil {
		return PreviewSpec{}, fmt.Errorf("decode preview: %w", err)
	}
	if err := ValidatePreview(spec); err != nil {
		return PreviewSpec{}, err
	}
	return spec, nil
}

// DecodeReviewResult decodes a result without accepting unknown fields or
// trailing JSON values.
func DecodeReviewResult(r io.Reader) (ReviewResult, error) {
	var result ReviewResult
	if err := decodeStrict(r, &result); err != nil {
		return ReviewResult{}, fmt.Errorf("decode review result: %w", err)
	}
	return result, nil
}

// ValidateReviewResult verifies host-agent output against a preview.
func ValidateReviewResult(result ReviewResult, spec PreviewSpec) error {
	if err := ValidatePreview(spec); err != nil {
		return err
	}
	if result.SchemaVersion != fmt.Sprint(ResultSchemaVersion) {
		return fmt.Errorf("unsupported result schema_version %q", result.SchemaVersion)
	}
	if result.SpecSHA256 != spec.SpecSHA256 {
		return fmt.Errorf("result spec_sha256 does not match the preview")
	}

	selected := make(map[string]PreviewFile)
	paths := make(map[string][]string)
	for _, file := range spec.Files {
		if !file.Reviewable {
			continue
		}
		selected[file.ID] = file
		paths[file.Path] = append(paths[file.Path], file.ID)
	}

	accounted := make(map[string]bool, len(selected))
	for _, file := range result.ReviewedFiles {
		if err := accountFile(accounted, selected, file); err != nil {
			return fmt.Errorf("reviewed_files: %w", err)
		}
	}
	for _, file := range result.SkippedFiles {
		if strings.TrimSpace(file.Reason) == "" {
			return fmt.Errorf("skipped_files: %s has an empty reason", file.ID)
		}
		if err := accountFile(accounted, selected, file.FileRef); err != nil {
			return fmt.Errorf("skipped_files: %w", err)
		}
	}
	if len(accounted) != len(selected) {
		return fmt.Errorf("coverage accounts for %d of %d selected files", len(accounted), len(selected))
	}

	seenFindings := make(map[string]bool, len(result.Findings))
	for index, finding := range result.Findings {
		if err := validateFinding(finding, selected, paths); err != nil {
			return fmt.Errorf("findings[%d]: %w", index, err)
		}
		key := fmt.Sprintf("%s\x00%d\x00%d\x00%s", finding.Path, finding.StartLine, finding.EndLine, finding.Content)
		if finding.FileID != "" {
			key = finding.FileID + "\x00" + key
		}
		if seenFindings[key] {
			return fmt.Errorf("findings[%d]: duplicate finding", index)
		}
		seenFindings[key] = true
	}

	expectedTotal := len(selected)
	expectedReviewed := len(result.ReviewedFiles)
	expectedSkipped := len(result.SkippedFiles)
	expectedRate := 1.0
	if expectedTotal > 0 {
		expectedRate = float64(expectedReviewed+expectedSkipped) / float64(expectedTotal)
	}
	if result.Coverage.TotalFiles != expectedTotal ||
		result.Coverage.ReviewedFiles != expectedReviewed ||
		result.Coverage.SkippedFiles != expectedSkipped ||
		math.Abs(result.Coverage.CoverageRate-expectedRate) > 0.000001 {
		return fmt.Errorf("coverage summary does not match accounted files")
	}
	return nil
}

func accountFile(accounted map[string]bool, selected map[string]PreviewFile, ref FileRef) error {
	if ref.ID == "" {
		return fmt.Errorf("file id is required")
	}
	file, ok := selected[ref.ID]
	if !ok {
		return fmt.Errorf("file id %q is not selected in the preview", ref.ID)
	}
	if accounted[ref.ID] {
		return fmt.Errorf("file id %q is accounted more than once", ref.ID)
	}
	if ref.Path != file.Path || ref.Status != file.Status {
		return fmt.Errorf("file id %q does not match preview metadata", ref.ID)
	}
	accounted[ref.ID] = true
	return nil
}

func validateFinding(finding Finding, selected map[string]PreviewFile, paths map[string][]string) error {
	if strings.TrimSpace(finding.Content) == "" {
		return fmt.Errorf("content is required")
	}
	if err := validateRelativePath(finding.Path); err != nil {
		return err
	}
	matchingIDs := paths[finding.Path]
	if len(matchingIDs) == 0 {
		return fmt.Errorf("path %q is not selected in the preview", finding.Path)
	}
	var file PreviewFile
	if finding.FileID != "" {
		candidate, ok := selected[finding.FileID]
		if !ok || candidate.Path != finding.Path {
			return fmt.Errorf("file_id %q does not match path %q", finding.FileID, finding.Path)
		}
		file = candidate
	} else if len(matchingIDs) > 1 {
		return fmt.Errorf("path %q is ambiguous; file_id is required", finding.Path)
	} else {
		file = selected[matchingIDs[0]]
	}
	if (finding.StartLine == 0) != (finding.EndLine == 0) {
		return fmt.Errorf("start_line and end_line must be supplied together")
	}
	if finding.StartLine < 0 || finding.EndLine < 0 || finding.EndLine < finding.StartLine {
		return fmt.Errorf("invalid line range")
	}
	if finding.StartLine > file.NewFileLines || finding.EndLine > file.NewFileLines {
		return fmt.Errorf("line range exceeds the file's %d lines", file.NewFileLines)
	}
	if finding.Severity != "" && !allowedSeverities[finding.Severity] {
		return fmt.Errorf("invalid severity %q", finding.Severity)
	}
	if finding.Category != "" && !allowedCategories[finding.Category] {
		return fmt.Errorf("invalid category %q", finding.Category)
	}
	return nil
}

func decodeStrict(r io.Reader, target any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
