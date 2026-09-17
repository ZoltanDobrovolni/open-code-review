// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegate

import (
	"testing"

	"github.com/alibaba/open-code-review/internal/config/rules"
	"github.com/alibaba/open-code-review/internal/model"
)

func TestSelectFilesAppliesStaticGates(t *testing.T) {
	filter := &rules.FileFilter{
		Include: []string{"included.go"},
		Exclude: []string{"excluded.go"},
	}
	cases := []struct {
		name     string
		diff     model.Diff
		want     model.ExcludeReason
		selected bool
	}{
		{name: "selected", diff: model.Diff{NewPath: "main.go"}, selected: true},
		{name: "user exclude wins", diff: model.Diff{NewPath: "excluded.go"}, want: model.ExcludeUserRule},
		{name: "include admits supported extension", diff: model.Diff{NewPath: "included.go"}, selected: true},
		{name: "include is additive", diff: model.Diff{NewPath: "other.go"}, selected: true},
		{name: "unsupported extension", diff: model.Diff{NewPath: "notes.txt"}, want: model.ExcludeExtension},
		{name: "default path", diff: model.Diff{NewPath: "foo_test.go"}, want: model.ExcludeDefaultPath},
		{name: "secret path cannot be included", diff: model.Diff{NewPath: ".env", IsNew: true}, want: model.ExcludeSecret},
		{name: "binary", diff: model.Diff{NewPath: "image.png", IsBinary: true}, want: model.ExcludeBinary},
		{name: "deleted", diff: model.Diff{OldPath: "old.go", NewPath: "/dev/null", IsDeleted: true}, want: model.ExcludeDeleted},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectFiles([]model.Diff{tc.diff}, filter)
			if len(got) != 1 {
				t.Fatalf("got %d decisions, want 1", len(got))
			}
			if got[0].Reason != tc.want {
				t.Errorf("reason = %q, want %q", got[0].Reason, tc.want)
			}
			if got[0].Selected() != tc.selected {
				t.Errorf("selected = %v, want %v", got[0].Selected(), tc.selected)
			}
		})
	}
}

func TestSelectFilesPreservesOrder(t *testing.T) {
	got := SelectFiles([]model.Diff{
		{NewPath: "first.go"},
		{NewPath: "second.go"},
	}, nil)
	if len(got) != 2 || got[0].Diff.NewPath != "first.go" || got[1].Diff.NewPath != "second.go" {
		t.Fatalf("decisions = %#v, want input order", got)
	}
}
