# gh-pr-review

## Install
- `go install ./...` (installs the binary as `gh-prr`)

## Usage
- Export review threads to Markdown:
  - `gh-prr export [pr_number] [-c N] [--unresolved-only]` (defaults to current branch PR)
- Resolve all unresolved review threads:
  - `gh-prr resolve [pr_number]` (defaults to current branch PR)
