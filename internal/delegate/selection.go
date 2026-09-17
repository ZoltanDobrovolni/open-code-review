// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegate

import (
	"path/filepath"
	"strings"

	allowedext "github.com/alibaba/open-code-review/internal/config/allowlist"
	"github.com/alibaba/open-code-review/internal/config/rules"
	"github.com/alibaba/open-code-review/internal/model"
)

// FileDecision is the deterministic delegated-review outcome for one diff.
type FileDecision struct {
	Diff   model.Diff
	Reason model.ExcludeReason
}

// Selected reports whether the file is eligible for host-agent review.
func (d FileDecision) Selected() bool {
	return d.Reason == model.ExcludeNone
}

// SelectFiles applies the static file gates used by delegated preview.
// Delegation intentionally does not apply the OCR LLM prompt-size limit.
func SelectFiles(diffs []model.Diff, filter *rules.FileFilter) []FileDecision {
	decisions := make([]FileDecision, 0, len(diffs))
	for _, diff := range diffs {
		decision := FileDecision{Diff: diff, Reason: whyExcluded(diff, filter)}
		if decision.Reason == model.ExcludeNone && diff.IsDeleted {
			decision.Reason = model.ExcludeDeleted
		}
		decisions = append(decisions, decision)
	}
	return decisions
}

func whyExcluded(diff model.Diff, filter *rules.FileFilter) model.ExcludeReason {
	if diff.IsBinary {
		return model.ExcludeBinary
	}

	path := effectivePath(diff)
	if allowedext.IsSecretPath(diff.OldPath) || allowedext.IsSecretPath(diff.NewPath) {
		return model.ExcludeSecret
	}
	if filter != nil && filter.IsUserExcluded(path) {
		return model.ExcludeUserRule
	}
	if filter != nil && filter.HasInclude() && filter.IsUserIncluded(path) {
		return model.ExcludeNone
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext != "" && !allowedext.IsAllowedExt(ext) {
		return model.ExcludeExtension
	}
	if allowedext.IsExcludedPath(path) {
		return model.ExcludeDefaultPath
	}
	return model.ExcludeNone
}

func effectivePath(diff model.Diff) string {
	if diff.NewPath == "/dev/null" {
		return diff.OldPath
	}
	return diff.NewPath
}
