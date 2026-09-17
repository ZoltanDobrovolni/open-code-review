// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alibaba/open-code-review/internal/config/rules"
	"github.com/alibaba/open-code-review/internal/diff"
	"github.com/alibaba/open-code-review/internal/gitcmd"
	"github.com/alibaba/open-code-review/internal/model"
)

const (
	PreviewSchemaVersion = 1
	DiffSchemaVersion    = 1
)

// PreviewOptions describes one deterministic review input.
type PreviewOptions struct {
	RepoDir     string
	From        string
	To          string
	Commit      string
	RulePath    string
	Excludes    []string
	Background  string
	MaxGitProcs int
}

// PreviewSpec is the immutable review scope handed to the host agent.
type PreviewSpec struct {
	SchemaVersion string        `json:"schema_version"`
	Mode          string        `json:"mode"`
	Repository    string        `json:"repository"`
	From          string        `json:"from,omitempty"`
	To            string        `json:"to,omitempty"`
	Commit        string        `json:"commit,omitempty"`
	MergeBase     string        `json:"merge_base,omitempty"`
	ResolvedBase  string        `json:"resolved_base,omitempty"`
	ResolvedHead  string        `json:"resolved_head,omitempty"`
	ExactRange    string        `json:"exact_range,omitempty"`
	Background    string        `json:"background,omitempty"`
	RulePath      string        `json:"rule_path,omitempty"`
	Excludes      []string      `json:"excludes,omitempty"`
	Files         []PreviewFile `json:"files"`
	SpecSHA256    string        `json:"spec_sha256"`
}

// PreviewFile is one changeset entry. ID remains stable for the lifetime of a
// preview, including when two entries have the same path.
type PreviewFile struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	OldPath       string `json:"old_path,omitempty"`
	NewPath       string `json:"new_path,omitempty"`
	Status        string `json:"status"`
	Insertions    int64  `json:"insertions"`
	Deletions     int64  `json:"deletions"`
	NewFileLines  int    `json:"new_file_lines"`
	DiffSHA256    string `json:"diff_sha256"`
	ContentSHA256 string `json:"content_sha256"`
	Reviewable    bool   `json:"reviewable"`
	ExcludeReason string `json:"exclude_reason,omitempty"`
}

// RuleOptions describes deterministic rule resolution for a set of paths.
type RuleOptions struct {
	RepoDir     string
	From        string
	To          string
	Commit      string
	RulePath    string
	MaxGitProcs int
}

// DiffBundle contains source and diff data for selected preview files.
type DiffBundle struct {
	SchemaVersion string     `json:"schema_version"`
	SpecSHA256    string     `json:"spec_sha256"`
	Files         []DiffFile `json:"files"`
}

// DiffFile is the structured representation of one selected file's change.
type DiffFile struct {
	ID             string `json:"id"`
	Path           string `json:"path"`
	OldPath        string `json:"old_path,omitempty"`
	NewPath        string `json:"new_path,omitempty"`
	Status         string `json:"status"`
	Diff           string `json:"diff,omitempty"`
	NewFileContent string `json:"new_file_content,omitempty"`
	Binary         bool   `json:"binary,omitempty"`
	Deleted        bool   `json:"deleted,omitempty"`
	Insertions     int64  `json:"insertions"`
	Deletions      int64  `json:"deletions"`
	DiffSHA256     string `json:"diff_sha256"`
	ContentSHA256  string `json:"content_sha256"`
}

// BuildPreview resolves the repository, rules, diffs, and delegated file
// selection without loading configuration for an LLM or creating a session.
func BuildPreview(ctx context.Context, opts PreviewOptions) (PreviewSpec, error) {
	repoDir, runner, err := resolveRepository(ctx, opts.RepoDir, opts.MaxGitProcs)
	if err != nil {
		return PreviewSpec{}, err
	}
	if err := validateRefs(ctx, repoDir, runner, opts.From, opts.To, opts.Commit); err != nil {
		return PreviewSpec{}, err
	}

	_, filter, err := loadResolver(repoDir, opts.RulePath, opts.From, opts.To, opts.Commit, runner)
	if err != nil {
		return PreviewSpec{}, err
	}
	if len(opts.Excludes) > 0 {
		if filter == nil {
			filter = &rules.FileFilter{}
		}
		filter.Exclude = append(filter.Exclude, opts.Excludes...)
	}

	provider := newProvider(repoDir, opts.From, opts.To, opts.Commit, runner)
	set, err := provider.GetDiffSet(ctx)
	if err != nil {
		return PreviewSpec{}, fmt.Errorf("load diffs: %w", err)
	}
	decisions := SelectFiles(set.Included, filter)
	input := provider.ResolveInput(ctx)

	spec := PreviewSpec{
		SchemaVersion: fmt.Sprint(PreviewSchemaVersion),
		Mode:          reviewMode(opts.From, opts.To, opts.Commit),
		Repository:    repoDir,
		From:          opts.From,
		To:            opts.To,
		Commit:        opts.Commit,
		Background:    opts.Background,
		RulePath:      opts.RulePath,
		Excludes:      append([]string(nil), opts.Excludes...),
		ResolvedBase:  input.ResolvedBase,
		ResolvedHead:  input.ResolvedHead,
		ExactRange:    input.ExactRange,
		Files:         make([]PreviewFile, 0, len(set.Included)+len(set.Excluded)),
	}
	if spec.Mode == "range" {
		spec.MergeBase = provider.MergeBase(ctx)
	}

	decisionIndex := 0
	fileIndex := 0
	set.ForEachInOrder(func(d model.Diff, providerExcluded bool) {
		fileIndex++
		file := PreviewFile{
			ID:            fileID(fileIndex),
			Path:          effectivePath(d),
			OldPath:       d.OldPath,
			NewPath:       d.NewPath,
			Status:        diffStatus(d),
			Insertions:    d.Insertions,
			Deletions:     d.Deletions,
			NewFileLines:  countFileLines(d.NewFileContent),
			DiffSHA256:    hashText(d.Diff),
			ContentSHA256: hashText(d.NewFileContent),
		}
		if providerExcluded {
			file.ExcludeReason = string(model.ExcludeProviderDirectory)
		} else {
			decision := decisions[decisionIndex]
			decisionIndex++
			file.Reviewable = decision.Selected()
			file.ExcludeReason = string(decision.Reason)
		}
		spec.Files = append(spec.Files, file)
	})
	spec.SpecSHA256 = hashPreview(spec)
	return spec, nil
}

// BuildRuleGroups resolves rules for paths without reading LLM configuration.
func BuildRuleGroups(ctx context.Context, opts RuleOptions, paths []string) ([]RuleGroup, error) {
	for _, path := range paths {
		if err := validateRelativePath(path); err != nil {
			return nil, err
		}
	}
	repoDir, runner, err := resolveRepository(ctx, opts.RepoDir, opts.MaxGitProcs)
	if err != nil {
		return nil, err
	}
	if err := validateRefs(ctx, repoDir, runner, opts.From, opts.To, opts.Commit); err != nil {
		return nil, err
	}
	resolver, _, err := loadResolver(repoDir, opts.RulePath, opts.From, opts.To, opts.Commit, runner)
	if err != nil {
		return nil, err
	}
	return GroupRules(resolver, paths), nil
}

// BuildDiffBundle reloads the local diff set and returns only files selected by
// a previously generated preview. Paths are filters, never source arguments.
func BuildDiffBundle(ctx context.Context, spec PreviewSpec, paths []string, maxGitProcs int) (DiffBundle, error) {
	if err := ValidatePreview(spec); err != nil {
		return DiffBundle{}, err
	}
	if err := VerifyPreviewCurrent(ctx, spec, maxGitProcs); err != nil {
		return DiffBundle{}, err
	}
	repoDir, runner, err := resolveRepository(ctx, spec.Repository, maxGitProcs)
	if err != nil {
		return DiffBundle{}, err
	}
	provider := newProvider(repoDir, spec.From, spec.To, spec.Commit, runner)
	set, err := provider.GetDiffSet(ctx)
	if err != nil {
		return DiffBundle{}, fmt.Errorf("load diffs: %w", err)
	}

	wanted := make(map[string]bool, len(paths))
	for _, path := range paths {
		if err := validateRelativePath(path); err != nil {
			return DiffBundle{}, err
		}
		wanted[path] = true
	}
	found := make(map[string]bool, len(wanted))
	specByID := make(map[string]PreviewFile, len(spec.Files))
	for _, file := range spec.Files {
		specByID[file.ID] = file
	}

	bundle := DiffBundle{
		SchemaVersion: fmt.Sprint(DiffSchemaVersion),
		SpecSHA256:    spec.SpecSHA256,
		Files:         make([]DiffFile, 0),
	}
	index := 0
	set.ForEachInOrder(func(d model.Diff, _ bool) {
		index++
		id := fileID(index)
		file, exists := specByID[id]
		if !exists || !file.Reviewable {
			return
		}
		if len(wanted) > 0 && !wanted[file.Path] {
			return
		}
		if len(wanted) > 0 {
			found[file.Path] = true
		}
		bundle.Files = append(bundle.Files, DiffFile{
			ID:             id,
			Path:           file.Path,
			OldPath:        d.OldPath,
			NewPath:        d.NewPath,
			Status:         file.Status,
			Diff:           d.Diff,
			NewFileContent: d.NewFileContent,
			Binary:         d.IsBinary,
			Deleted:        d.IsDeleted,
			Insertions:     d.Insertions,
			Deletions:      d.Deletions,
			DiffSHA256:     hashText(d.Diff),
			ContentSHA256:  hashText(d.NewFileContent),
		})
	})
	for path := range wanted {
		if !found[path] {
			return DiffBundle{}, fmt.Errorf("path %q is not a selected file in the preview", path)
		}
	}
	return bundle, nil
}

// ValidatePreview checks the envelope and its self-digest before it is used as
// an authority for subsequent diff retrieval or result validation.
func ValidatePreview(spec PreviewSpec) error {
	if spec.SchemaVersion != fmt.Sprint(PreviewSchemaVersion) {
		return fmt.Errorf("unsupported preview schema_version %q", spec.SchemaVersion)
	}
	if spec.Repository == "" {
		return fmt.Errorf("preview repository is required")
	}
	if spec.SpecSHA256 == "" || spec.SpecSHA256 != hashPreview(spec) {
		return fmt.Errorf("preview spec_sha256 does not match its contents")
	}
	seenIDs := make(map[string]bool, len(spec.Files))
	for index, file := range spec.Files {
		if file.ID != fileID(index+1) {
			return fmt.Errorf("preview file %d has invalid id %q", index, file.ID)
		}
		if seenIDs[file.ID] {
			return fmt.Errorf("preview contains duplicate file id %q", file.ID)
		}
		seenIDs[file.ID] = true
		if err := validateRelativePath(file.Path); err != nil {
			return fmt.Errorf("preview file %q: %w", file.ID, err)
		}
		if file.NewFileLines < 0 {
			return fmt.Errorf("preview file %q has negative line count", file.ID)
		}
	}
	return nil
}

// VerifyPreviewCurrent rebuilds the deterministic preview and rejects a
// workspace that changed after the original preview was generated.
func VerifyPreviewCurrent(ctx context.Context, spec PreviewSpec, maxGitProcs int) error {
	current, err := BuildPreview(ctx, PreviewOptions{
		RepoDir:     spec.Repository,
		From:        spec.From,
		To:          spec.To,
		Commit:      spec.Commit,
		RulePath:    spec.RulePath,
		Excludes:    append([]string(nil), spec.Excludes...),
		Background:  spec.Background,
		MaxGitProcs: maxGitProcs,
	})
	if err != nil {
		return fmt.Errorf("rebuild preview: %w", err)
	}
	if current.SpecSHA256 != spec.SpecSHA256 {
		return fmt.Errorf("review scope changed after preview; generate a new preview")
	}
	return nil
}

func resolveRepository(ctx context.Context, input string, maxGitProcs int) (string, *gitcmd.Runner, error) {
	if input == "" {
		var err error
		input, err = os.Getwd()
		if err != nil {
			return "", nil, fmt.Errorf("get working directory: %w", err)
		}
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", nil, fmt.Errorf("resolve repository path: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", nil, fmt.Errorf("stat repository %q: %w", abs, err)
	}
	runner := gitcmd.New(maxGitProcs)
	out, err := runner.Output(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", nil, fmt.Errorf("%s is not a git repository", abs)
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", nil, fmt.Errorf("git returned an empty repository root for %q", abs)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", nil, fmt.Errorf("resolve git repository root: %w", err)
	}
	return root, runner, nil
}

func loadResolver(repoDir, rulePath, from, to, commit string, runner *gitcmd.Runner) (rules.Resolver, *rules.FileFilter, error) {
	ref := ""
	if commit != "" {
		ref = commit
	} else if to != "" {
		ref = to
	}
	resolver, filter, err := rules.NewResolver(repoDir, rulePath, rules.ResolverOptions{Ref: ref, Runner: runner})
	if err != nil {
		return nil, nil, fmt.Errorf("load rules: %w", err)
	}
	return resolver, filter, nil
}

func validateRefs(ctx context.Context, repoDir string, runner *gitcmd.Runner, from, to, commit string) error {
	refs := []struct {
		name string
		ref  string
	}{
		{name: "--from", ref: from},
		{name: "--to", ref: to},
		{name: "--commit", ref: commit},
	}
	for _, item := range refs {
		if item.ref == "" {
			continue
		}
		if strings.HasPrefix(item.ref, "-") {
			return fmt.Errorf("%s value %q is not a valid git ref", item.name, item.ref)
		}
		if _, err := runner.Output(ctx, repoDir, "rev-parse", "--verify", "--quiet", "--end-of-options", item.ref+"^{commit}"); err != nil {
			return fmt.Errorf("%s value %q is not a valid commit ref", item.name, item.ref)
		}
	}
	if (from == "") != (to == "") {
		return fmt.Errorf("--from and --to must be supplied together")
	}
	if commit != "" && (from != "" || to != "") {
		return fmt.Errorf("--commit cannot be combined with --from or --to")
	}
	return nil
}

func newProvider(repoDir, from, to, commit string, runner *gitcmd.Runner) *diff.Provider {
	switch {
	case commit != "":
		return diff.NewCommitProvider(repoDir, commit, runner)
	case from != "" && to != "":
		return diff.NewProvider(repoDir, from, to, runner)
	default:
		return diff.NewWorkspaceProvider(repoDir, runner)
	}
}

func reviewMode(from, to, commit string) string {
	switch {
	case commit != "":
		return "commit"
	case from != "" && to != "":
		return "range"
	default:
		return "workspace"
	}
}

func fileID(index int) string {
	return fmt.Sprintf("file-%04d", index)
}

func diffStatus(d model.Diff) string {
	switch {
	case d.IsBinary:
		return "binary"
	case d.IsNew:
		return "added"
	case d.IsDeleted:
		return "deleted"
	case d.IsRenamed || (d.OldPath != d.NewPath && d.OldPath != "" && d.OldPath != "/dev/null"):
		return "renamed"
	default:
		return "modified"
	}
}

func hashPreview(spec PreviewSpec) string {
	spec.SpecSHA256 = ""
	data, _ := json.Marshal(spec)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func hashText(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func countFileLines(content string) int {
	if content == "" {
		return 0
	}
	lines := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		lines++
	}
	return lines
}

func validateRelativePath(path string) error {
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
		return fmt.Errorf("path %q is not a repository-relative path", path)
	}
	normalized := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") {
		return fmt.Errorf("path %q escapes the repository", path)
	}
	return nil
}
