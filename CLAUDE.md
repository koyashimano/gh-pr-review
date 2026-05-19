# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`gh-prr` is a Go CLI tool for working with GitHub PR review threads. It uses the `gh` CLI under the hood to call the GitHub GraphQL API. Subcommands:

- **export**: Fetch review threads and output as Markdown (`gh-prr export [-c N] [--include-resolved] [pr_number]`). Resolved threads are skipped by default; pass `--include-resolved` to include them.
- **resolve**: Resolve all unresolved review threads in parallel (`gh-prr resolve [-r REVIEWER]... [pr_number]`). Pass `-r`/`--reviewer` (repeatable, comma-separated values also accepted, case-insensitive) to limit to threads started by the given reviewer(s). The special value `@me` expands to the authenticated user (looked up via the `viewer { login }` GraphQL query).
- **pending**: Show the current user's pending (unsubmitted) review comments (`gh-prr pending [-c N] [pr_number]`)
- **wait**: Poll a PR for new reviews and exit when one is detected (`gh-prr wait [-i N] [-t N] [pr_number]`)
- **submit**: Submit a review from a single Markdown file (`gh-prr submit -f <file> [--finalize] [pr_number]`). Saves as a pending (draft) review by default; pass `--finalize` to publish immediately. See [docs/REVIEW_FORMAT.md](docs/REVIEW_FORMAT.md) for the file format.
- **submit-pending**: Submit an existing pending (draft) review (`gh-prr submit-pending [-e APPROVE|REQUEST_CHANGES|COMMENT] [pr_number]`)
- **viewed**: Mark (or with `-u`/`--unmark`, unmark) PR files as Viewed by path pattern in parallel (`gh-prr viewed [-u] [-n] <pattern>... [pr_number]`). Patterns are globs with `*`, `?`, and `**` (matches zero or more path segments).

Flags must precede the optional `pr_number` (Go's `flag` package stops parsing at the first non-flag argument).

All commands default to the PR associated with the current branch if no PR number is given.

## Build & Run

```bash
go build -o gh-prr ./cmd/gh-prr     # build
go install ./cmd/gh-prr              # install to $GOPATH/bin
go vet ./...                         # lint
```

Prerequisite: `gh` CLI must be installed and authenticated (`gh auth login`).

## Architecture

Single-file application: `cmd/gh-prr/main.go`. No external Go dependencies (stdlib only).

Key flow:
1. `parseArgs()` → parse subcommand and flags
2. `getOwnerRepo()` → detect repo via `gh repo view`
3. `resolvePRNumber()` → resolve PR number from arg or current branch via `gh pr view`
4. For **export**: `fetchThreads()` → paginate GraphQL reviewThreads (+ `fetchAllComments()` for overflow) → `renderMarkdown()`
5. For **resolve**: `fetchUnresolvedThreadIDs()` → `resolveAllThreads()` with concurrent goroutines (max 10) calling `resolveThread()` mutation
6. For **pending**: `fetchPendingReview()` → fetch PENDING state reviews via GraphQL (+ `fetchAllPendingReviewComments()` for overflow) → `renderPendingMarkdown()`
7. For **wait**: `fetchReviewSummary()` via GraphQL (`gh api graphql`) → poll in loop with `time.Sleep` → print latest review summary on detection
8. For **viewed**: `fetchPRFiles()` → paginate GraphQL `pullRequest.files` (path + `viewerViewedState`) → filter by glob patterns (`matchPathGlob` supports `*`, `?`, and `**` as a whole segment) → skip files already in the target state → concurrently (max 10) call `markFileAsViewed` / `unmarkFileAsViewed` mutation via `setFileViewed()`

All GitHub GraphQL API calls go through `run()` → `ghJSON()`, which shells out to `gh api graphql`. Repository and PR discovery use other `gh` subcommands such as `gh repo view` and `gh pr view`.
