# Review Hand-off: Deterministic Local Review via ocr-local + GitHub Copilot

- Date: 2026-09-16
- Scope: `skills/open-code-review-copilot/SKILL.md`, `.github/skills/open-code-review-copilot/SKILL.md`,
  `scripts/run-ocr-local.js`, `scripts/create-ocr-local-manifest.js`, `scripts/check-ocr-local-deps.js`,
  `cmd/ocr-local/main.go`, `internal/delegate/*`, `internal/gitcmd/runner.go`, `Makefile`, `package.json`, `.gitignore`
- Background: corporate IP protection — source code must never leave for third-party AI providers;
  GitHub Copilot is the only approved AI agent. The `ocr-local` binary prepares review data deterministically;
  Copilot performs the actual review.
- Method: static read-through only (no build, lint, or test runs, per review-process instructions).

## Verdict

The design is sound and matches the stated threat model: all Git access is local subprocess
(`exec.CommandContext` with argument arrays — no shell), refs are validated before use, JSON I/O is
strictly decoded, outputs are written `0600` into the gitignored `.ocr-local/`, the preview spec is
self-hashed and re-verified before diff retrieval and result validation, and the skill text explicitly
warns the agent to treat repository rules and file contents as data, not instructions (prompt-injection
aware). The findings below are hardening gaps, not fundamental flaws.

## Findings

### F1 — Medium (process/security): the "no LLM dependency" invariant is not enforced anywhere

`scripts/check-ocr-local-deps.js` is the control that proves `ocr-local` can never import an OCR
provider package (`internal/llm`, `internal/mcp`, provider SDKs, ...). It is exposed only as the npm
script `check:local-deps` and is referenced nowhere else: not in `Makefile` (`check`, `build-local`),
not in any GitHub workflow. A future PR that adds `internal/llm` to the `ocr-local` dependency tree
would build, pass `make check` and CI, and ship silently — breaking the core guarantee of this
workflow.

Recommendation: invoke it from `make check` (or a CI job) and from the `build-local` target.

### F2 — Medium (skill maintenance): the two SKILL.md copies have drifted

`skills/open-code-review-copilot/SKILL.md` and `.github/skills/open-code-review-copilot/SKILL.md`
differ: the `.github` copy (the one Copilot actually loads) contains a duplicated H1 heading
("# Copilot-only Open Code Review" twice) and uses different list indentation. There is no sync
mechanism or CI check. Drift between the npm-published skill and the repo-loaded skill will produce
divergent agent behavior depending on the entry point.

Recommendation: keep one source of truth and generate/copy the other in a script, or add a CI
equality check.

### F3 — Low (robustness): TOCTOU window between staleness check and diff read

`BuildDiffBundle` (and `delegate validate`) call `VerifyPreviewCurrent` and then re-run
`GetDiffSet` as separate steps (`internal/delegate/spec.go`). A workspace change landing between
the two git invocations would be returned silently: the bundle recomputes `DiffSHA256` /
`ContentSHA256` but never compares them against the values stored in the preview, so the mismatch
is detectable only if the host agent does the comparison itself — which the SKILL.md workflow does
not instruct.

Recommendation: inside `BuildDiffBundle`, reject a file whose recomputed hashes differ from the
preview's, or at minimum document that the host agent must compare hashes.

### F4 — Low (schema/docs mismatch): severity/category optional in code, implied required in skill

`validateFinding` in `internal/delegate/validate.go` accepts empty `severity` and `category`
(`if finding.Severity != "" && ...`), while the SKILL.md result schema shows both as always-present
fields. A Copilot result omitting them passes validation, weakening downstream reporting.

Related: `Finding.ID` exists in the Go struct (`id,omitempty`) but is never validated for
uniqueness and is absent from the SKILL.md schema — either document it or remove it (currently dead
weight in the contract).

### F5 — Low (cross-platform friction): Makefile binary name vs Windows instructions

`make build-local` always outputs `dist/ocr-local` (no `.exe`), while the SKILL.md Windows fallback
builds `dist/ocr-local.exe`. An extensionless PE runs on Windows, so this is not broken, but a
Windows developer with make installed gets a differently named artifact than the manifest example
shows, and `path.relative`-based manifest entries will encode whichever name was used.

Recommendation: parameterize the Makefile output name per OS, or document the extensionless name.

### F6 — Low (brittle policy check): substring matching and manual provider list

`check-ocr-local-deps.js` matches `dep.includes(item)`, which can false-positive on unrelated
module paths containing e.g. `github.com/aws/`, and the provider-SDK blocklist is a hand-maintained
list: every new provider added to the main CLI must be remembered here (shotgun-surgery risk).
Consider matching on path segments and/or deriving the forbidden `internal/*` set from a single
declared policy.

### F7 — Nit: `--max-git-procs 0` silently means 16

`validateModeOptions` accepts 0 as non-negative, but `gitcmd.New(0)` substitutes the default of 16.
Either reject 0 or document the fallback in the flag help.

### F8 — Nit: duplicate `--manifest` flags silently last-win

`parseInvocation` in `scripts/run-ocr-local.js` accepts `--manifest a --manifest b -- ...` and
uses `b` without complaint. Fail on repeats to catch operator error.

### F9 — Nit: ignored marshal error in `hashPreview`

`data, _ := json.Marshal(spec)` in `internal/delegate/spec.go`. It cannot fail for the current
struct shape (strings/ints/bools/slices), so this is defensive only, but returning the error would
make a future incompatible field addition fail loudly instead of producing a stable-but-wrong hash.

### F10 — Nit: zero-file reviews report 100% coverage

When no files are selected, `expectedRate` defaults to `1.0`, so an empty changeset validates with
`coverage_rate: 1.0`. Harmless, but consumers rendering "100% coverage" for an empty review may
mislead; consider `0.0` or an explicit empty-review marker.

## Process-level remarks (inherent to the design, not code bugs)

- `delegate validate` proves schema correctness and coverage completeness — it cannot prove that
  Copilot actually reviewed the content, nor the quality of findings. The SKILL.md gate
  "report findings only after validation succeeds" should be read as a completeness check; review
  diligence remains the operator's responsibility.
- The manifest is self-certified: it lives in the gitignored `.ocr-local/`, so there is no shared,
  reviewable pin. The trust anchor is "I built this binary from source I control." That fits the
  stated threat model (protecting IP from third parties, not from the developer) but is worth
  stating explicitly in the skill.
- `manifest.binary` is resolved relative to the manifest and may point anywhere on disk; since the
  manifest itself is the root of trust, this is acceptable, but it means the SHA-256 check protects
  against accidental binary drift, not against a malicious manifest.

## Positive notes (verified, no action needed)

- No shell anywhere in the Git path: `gitcmd.Runner` and providers use argument arrays; refs are
  rejected when starting with `-` and verified via `rev-parse --end-of-options`; path arguments are
  validated as repository-relative with escape checks.
- `--background-file` content is size-capped (1 MiB) and folded into the spec hash, so background
  changes invalidate stale previews.
- Secret-path exclusion is evaluated before user include rules in `internal/delegate/selection.go`,
  so a custom include rule can widen the extension allowlist but can never force-review a secret
  file — exactly right for the IP-protection goal.
- Strict JSON decoding (`DisallowUnknownFields` + trailing-value rejection) for both preview and
  result; duplicate-finding and duplicate-file-accounting detection; ambiguous-path requires
  `file_id`.
- `.ocr-local/` is in `.gitignore`; all generated artifacts are written `0600`.
- License headers present on all reviewed source files; all reviewed content is English-only.
