# gh-pr-review

## Install

- Prerequisite: [GitHub CLI (`gh`)](https://cli.github.com/) must be installed and authenticated (for example, via `gh auth login`).

- `go install ./...` (installs the binary as `gh-prr`)

## Usage

- Export review threads to Markdown:
  - `gh-prr export [pr_number] [-c N] [--unresolved-only]` (defaults to current branch PR)
- Resolve all unresolved review threads:
  - `gh-prr resolve [pr_number]` (defaults to current branch PR)
- Show pending (unsubmitted) review comments:
  - `gh-prr pending [pr_number] [-c N]` (defaults to current branch PR)
- Wait for a new review on a PR:
  - `gh-prr wait [pr_number] [-i N] [-t N]` (poll every N seconds, timeout after N seconds; defaults: 30s interval, 900s timeout)
