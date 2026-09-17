// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import "testing"

func TestValidateModeOptions(t *testing.T) {
	cases := []struct {
		name string
		opts modeOptions
		want bool
	}{
		{name: "workspace", opts: modeOptions{format: "json", maxGitProcs: 16}},
		{name: "range", opts: modeOptions{from: "main", to: "feature", format: "json", maxGitProcs: 16}},
		{name: "commit", opts: modeOptions{commit: "abc", format: "json", maxGitProcs: 16}},
		{name: "missing to", opts: modeOptions{from: "main", format: "json", maxGitProcs: 16}, want: true},
		{name: "mixed modes", opts: modeOptions{commit: "abc", from: "main", to: "feature", format: "json", maxGitProcs: 16}, want: true},
		{name: "unsupported format", opts: modeOptions{format: "text", maxGitProcs: 16}, want: true},
		{name: "negative git limit", opts: modeOptions{format: "json", maxGitProcs: -1}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateModeOptions(tc.opts) != nil; got != tc.want {
				t.Errorf("validateModeOptions() error = %v, want error %v", got, tc.want)
			}
		})
	}
}

func TestSplitPatterns(t *testing.T) {
	got := splitPatterns(" **/*.go, ,vendor/** ")
	if len(got) != 2 || got[0] != "**/*.go" || got[1] != "vendor/**" {
		t.Fatalf("splitPatterns() = %#v", got)
	}
}
