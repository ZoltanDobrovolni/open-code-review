// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package llmrelocation

import "testing"

func TestExtractCodeBlock(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"with language tag", "```go\nfoo\nbar\n```", "foo\nbar"},
		{"without language tag", "```\nfoo\n```", "foo"},
		{"with surrounding text", "Here:\n```\ncode\n```\ndone", "code"},
		{"no code block", "just text", ""},
		{"empty block", "```\n```", ""},
		{"opening fence without newline", "```go", ""},
		{"no closing fence", "```\nfoo\nbar", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractCodeBlock(tt.input)
			if got != tt.want {
				t.Errorf("extractCodeBlock() = %q, want %q", got, tt.want)
			}
		})
	}
}
