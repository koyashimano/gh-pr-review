# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`gh-prr` is a Go CLI tool for working with GitHub PR review threads. It uses the `gh` CLI under the hood to call the GitHub GraphQL API. Two subcommands:

- **export**: Fetch review threads and output as Markdown (`gh-prr export [pr_number] [-c N] [--unresolved-only]`)
- **resolve**: Resolve all unresolved review threads in parallel (`gh-prr resolve [pr_number]`)

Both commands default to the PR associated with the current branch if no PR number is given.

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

All GitHub API calls go through `run()` → `ghJSON()` which shells out to `gh api graphql`.
