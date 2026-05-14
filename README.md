# gh-pr-review

## Install

- Prerequisite: [GitHub CLI (`gh`)](https://cli.github.com/) must be installed and authenticated (for example, via `gh auth login`).

- `go install ./...` (installs the binary as `gh-prr`)

## Usage

Flags must come before the optional `pr_number`. The `pr_number` defaults to the PR for the current branch.

- Export review threads to Markdown:
  - `gh-prr export [-c N] [--include-resolved] [pr_number]` (skips resolved threads by default; add `--include-resolved` to keep them)
- Resolve all unresolved review threads:
  - `gh-prr resolve [pr_number]`
- Show pending (unsubmitted) review comments:
  - `gh-prr pending [-c N] [pr_number]`
- Wait for a new review on a PR:
  - `gh-prr wait [-i N|-interval N] [-t N|-timeout N] [pr_number]` (poll every N seconds, timeout after N seconds; defaults: 30s interval, 900s timeout)
- Submit a review from a single Markdown file:
  - `gh-prr submit -f review.md [--pending] [pr_number]` (use `--pending` to leave the review as a draft; see [docs/REVIEW_FORMAT.md](docs/REVIEW_FORMAT.md) for the file format)
- Submit your existing pending review:
  - `gh-prr submit-pending [-e APPROVE|REQUEST_CHANGES|COMMENT] [pr_number]` (defaults to `COMMENT`)
- Mark (or unmark) PR files as Viewed by path pattern:
  - `gh-prr viewed [-u|--unmark] [-n|--dry-run] <pattern>... [pr_number]`
  - Patterns are globs that support `*`, `?`, and `**` (matches zero or more path segments). Multiple patterns may be passed.
  - Example: `gh-prr viewed '**/testdata/**' '**/*_test.go'` marks fixtures under any `testdata/` directory and all test files as Viewed before review. (`testdata/**` alone only matches a top-level `testdata` directory.)
