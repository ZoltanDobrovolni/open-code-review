// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alibaba/open-code-review/internal/delegate"
	"github.com/spf13/cobra"
)

type modeOptions struct {
	repo        string
	from        string
	to          string
	commit      string
	rule        string
	exclude     string
	format      string
	output      string
	background  string
	maxGitProcs int
}

type ruleEnvelope struct {
	SchemaVersion string          `json:"schema_version"`
	Groups        []ruleGroupJSON `json:"groups"`
}

type ruleGroupJSON struct {
	GroupID int      `json:"group_id"`
	Source  string   `json:"source"`
	Pattern string   `json:"pattern"`
	Files   []string `json:"files"`
	Rule    string   `json:"rule"`
}

type validationEnvelope struct {
	Valid         bool   `json:"valid"`
	SchemaVersion string `json:"schema_version"`
	SpecSHA256    string `json:"spec_sha256"`
}

var Version = "dev"

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "ocr-local",
		Short:         "Deterministic local review preparation",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			showVersion, err := cmd.Flags().GetBool("version")
			if err != nil {
				return err
			}
			if showVersion {
				fmt.Printf("ocr-local %s\n", Version)
				return nil
			}
			return cmd.Help()
		},
	}
	root.Flags().BoolP("version", "V", false, "print the local binary version")
	root.AddCommand(newDelegateCommand())
	return root
}

func newDelegateCommand() *cobra.Command {
	delegateCmd := &cobra.Command{
		Use:   "delegate",
		Short: "Prepare local review data without an AI provider",
		Args:  cobra.NoArgs,
	}
	delegateCmd.AddCommand(newPreviewCommand())
	delegateCmd.AddCommand(newRuleCommand())
	delegateCmd.AddCommand(newDiffCommand())
	delegateCmd.AddCommand(newValidateCommand())
	return delegateCmd
}

func newPreviewCommand() *cobra.Command {
	var opts modeOptions
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Write an immutable review scope",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateModeOptions(opts); err != nil {
				return err
			}
			background, err := readBackground(opts.background)
			if err != nil {
				return err
			}
			spec, err := delegate.BuildPreview(cmd.Context(), delegate.PreviewOptions{
				RepoDir:     opts.repo,
				From:        opts.from,
				To:          opts.to,
				Commit:      opts.commit,
				RulePath:    opts.rule,
				Excludes:    splitPatterns(opts.exclude),
				Background:  background,
				MaxGitProcs: opts.maxGitProcs,
			})
			if err != nil {
				return err
			}
			return writeJSON(opts.format, opts.output, spec)
		},
	}
	addModeFlags(cmd, &opts)
	cmd.Flags().StringVarP(&opts.background, "background-file", "B", "", "read review context from a local file")
	return cmd
}

func newRuleCommand() *cobra.Command {
	var opts modeOptions
	cmd := &cobra.Command{
		Use:   "rule <path...>",
		Short: "Resolve review rules for repository paths",
		Args:  minimumArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateModeOptions(opts); err != nil {
				return err
			}
			groups, err := delegate.BuildRuleGroups(cmd.Context(), delegate.RuleOptions{
				RepoDir:     opts.repo,
				From:        opts.from,
				To:          opts.to,
				Commit:      opts.commit,
				RulePath:    opts.rule,
				MaxGitProcs: opts.maxGitProcs,
			}, args)
			if err != nil {
				return err
			}
			serialized := make([]ruleGroupJSON, 0, len(groups))
			for _, group := range groups {
				serialized = append(serialized, ruleGroupJSON{
					GroupID: group.ID,
					Source:  group.Source,
					Pattern: group.Pattern,
					Files:   append([]string{}, group.Files...),
					Rule:    group.Text,
				})
			}
			return writeJSON(opts.format, opts.output, ruleEnvelope{
				SchemaVersion: "1",
				Groups:        serialized,
			})
		},
	}
	addModeFlags(cmd, &opts)
	return cmd
}

func newDiffCommand() *cobra.Command {
	var specPath string
	var paths []string
	var format string
	var output string
	var maxGitProcs int
	cmd := &cobra.Command{
		Use:   "diff --spec <preview.json> [--path <path>]...",
		Short: "Write selected diffs and new-file content",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(specPath) == "" {
				return errors.New("--spec is required")
			}
			spec, err := readPreview(specPath)
			if err != nil {
				return err
			}
			bundle, err := delegate.BuildDiffBundle(cmd.Context(), spec, paths, maxGitProcs)
			if err != nil {
				return err
			}
			return writeJSON(format, output, bundle)
		},
	}
	cmd.Flags().StringVar(&specPath, "spec", "", "preview JSON file")
	cmd.Flags().StringArrayVar(&paths, "path", nil, "selected repository-relative path; repeat for multiple paths")
	cmd.Flags().StringVarP(&format, "format", "f", "json", "output format; only json is supported")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write JSON to a file instead of stdout")
	cmd.Flags().IntVar(&maxGitProcs, "max-git-procs", 16, "maximum concurrent local git processes")
	return cmd
}

func newValidateCommand() *cobra.Command {
	var specPath string
	var inputPath string
	var format string
	var output string
	var maxGitProcs int
	cmd := &cobra.Command{
		Use:   "validate --spec <preview.json> [--input <result.json>]",
		Short: "Validate host-agent findings against a preview",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(specPath) == "" {
				return errors.New("--spec is required")
			}
			spec, err := readPreview(specPath)
			if err != nil {
				return err
			}
			if err := delegate.VerifyPreviewCurrent(cmd.Context(), spec, maxGitProcs); err != nil {
				return err
			}
			resultReader, closeResult, err := openInput(inputPath)
			if err != nil {
				return err
			}
			defer closeResult()
			result, err := delegate.DecodeReviewResult(resultReader)
			if err != nil {
				return err
			}
			if err := delegate.ValidateReviewResult(result, spec); err != nil {
				return err
			}
			return writeJSON(format, output, validationEnvelope{
				Valid:         true,
				SchemaVersion: result.SchemaVersion,
				SpecSHA256:    spec.SpecSHA256,
			})
		},
	}
	cmd.Flags().StringVar(&specPath, "spec", "", "preview JSON file")
	cmd.Flags().StringVarP(&inputPath, "input", "i", "-", "review result JSON file; '-' reads stdin")
	cmd.Flags().StringVarP(&format, "format", "f", "json", "output format; only json is supported")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write JSON to a file instead of stdout")
	cmd.Flags().IntVar(&maxGitProcs, "max-git-procs", 16, "maximum concurrent local git processes")
	return cmd
}

func addModeFlags(cmd *cobra.Command, opts *modeOptions) {
	cmd.Flags().StringVar(&opts.repo, "repo", "", "root directory of the git repository")
	cmd.Flags().StringVar(&opts.from, "from", "", "source commit ref for range mode")
	cmd.Flags().StringVar(&opts.to, "to", "", "target commit ref for range mode")
	cmd.Flags().StringVarP(&opts.commit, "commit", "c", "", "single commit ref")
	cmd.Flags().StringVar(&opts.rule, "rule", "", "custom rule JSON path")
	cmd.Flags().StringVar(&opts.exclude, "exclude", "", "comma-separated repository-relative exclude patterns")
	cmd.Flags().StringVarP(&opts.format, "format", "f", "json", "output format; only json is supported")
	cmd.Flags().StringVarP(&opts.output, "output", "o", "", "write JSON to a file instead of stdout")
	cmd.Flags().IntVar(&opts.maxGitProcs, "max-git-procs", 16, "maximum concurrent local git processes")
}

func validateModeOptions(opts modeOptions) error {
	if opts.format != "json" {
		return fmt.Errorf("invalid --format value %q: only json is supported", opts.format)
	}
	if (opts.from == "") != (opts.to == "") {
		return errors.New("--from and --to must be supplied together")
	}
	if opts.commit != "" && (opts.from != "" || opts.to != "") {
		return errors.New("--commit cannot be combined with --from or --to")
	}
	if opts.maxGitProcs < 0 {
		return errors.New("--max-git-procs must be non-negative")
	}
	return nil
}

func minimumArgs(count int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) < count {
			return fmt.Errorf("requires at least %d argument(s)", count)
		}
		return nil
	}
}

func splitPatterns(raw string) []string {
	var patterns []string
	for _, pattern := range strings.Split(raw, ",") {
		if pattern = strings.TrimSpace(pattern); pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	return patterns
}

func readBackground(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read background file: %w", err)
	}
	const maxBackgroundBytes = 1 << 20
	if len(data) > maxBackgroundBytes {
		return "", fmt.Errorf("background file exceeds %d bytes", maxBackgroundBytes)
	}
	return string(data), nil
}

func readPreview(path string) (delegate.PreviewSpec, error) {
	reader, closeReader, err := openInput(path)
	if err != nil {
		return delegate.PreviewSpec{}, err
	}
	defer closeReader()
	return delegate.DecodePreview(reader)
}

func openInput(path string) (io.Reader, func(), error) {
	if path == "" || path == "-" {
		return os.Stdin, func() {}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open input %q: %w", path, err)
	}
	return file, func() { _ = file.Close() }, nil
}

func writeJSON(format, path string, value any) error {
	if format != "json" {
		return fmt.Errorf("invalid --format value %q: only json is supported", format)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	if path == "" || path == "-" {
		_, err := os.Stdout.Write(buffer.Bytes())
		return err
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write output %q: %w", path, err)
	}
	return nil
}
