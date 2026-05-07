# Review Markdown Format

`gh-prr submit` reads a single Markdown file describing a pull request review and posts it to GitHub. This document defines the file format.

## File structure

```markdown
---
event: COMMENT
commit: a1b2c3d4
---

Overall summary of the review.
Multiple lines are allowed.

## path/to/file.go:42

Inline comment on line 42.

## path/to/file.go:10-15

Multi-line inline comment covering lines 10 through 15.

## docs/note.md:5 [side=LEFT]

Comment on the pre-change (deleted) side of the diff at line 5.

## file: docs/note.md

Comment that applies to the whole file rather than a specific line.
```

A review file consists of three sections, in order:

1. An optional **front matter** block.
2. An optional **review body** (summary).
3. Zero or more **inline comment** sections.

Either the body or at least one inline comment is required when `event` is `COMMENT` or `REQUEST_CHANGES`. `APPROVE` may have an empty body. Pending reviews (`--pending`) may be empty.

## Front matter

If the very first line of the file is `---`, the parser reads lines until the next `---` as front matter.

- Encoding: a small subset of YAML — one `key: value` pair per line.
- Blank lines and lines starting with `#` are ignored inside the block.
- Values may be wrapped in single or double quotes; the quotes are stripped.

Recognised keys:

| Key | Required | Description |
| --- | --- | --- |
| `event` | no | One of `APPROVE`, `REQUEST_CHANGES`, `COMMENT`. Defaults to `COMMENT`. Ignored when `--pending` is passed. |
| `commit` | no | Commit SHA the review applies to. Defaults to the PR's latest HEAD commit. |

Unknown keys cause a parse error so that typos don't go unnoticed.

## Review body

Everything between the end of the front matter (or the start of the file, if there is none) and the first inline comment header is the review body. Leading and trailing blank lines are trimmed. The body is sent verbatim to GitHub as the review's summary, so Markdown formatting (lists, code fences, links, etc.) is preserved.

## Inline comment header

Each inline comment starts with an H2 header that locates the comment.

### Line-anchored comment

```
## <path>:<line>
## <path>:<start_line>-<line>
## <path>:<line> [side=LEFT]
## <path>:<start_line>-<line> [side=LEFT, start_side=LEFT]
```

Rules:

- `<path>` must not contain `:`. GitHub uses forward slashes; backslashes are not supported.
- `<line>` is the line number in the file at the reviewed commit.
- For a multi-line comment, `<start_line>-<line>` specifies a range. `start_line` must be strictly less than `line`.
- The optional bracketed attribute list contains comma-separated `key=value` pairs. Whitespace around keys, values, and commas is ignored.

Recognised attribute keys:

| Key | Values | Default | Notes |
| --- | --- | --- | --- |
| `side` | `LEFT`, `RIGHT` | `RIGHT` | `LEFT` targets a deleted line on the pre-change side of the diff. |
| `start_side` | `LEFT`, `RIGHT` | same as `side` | Only meaningful for multi-line ranges. |

### File-level comment

```
## file: <path>
```

A header in this form attaches the comment to the whole file rather than a specific line. The `file:` prefix is literal and must be followed by at least one space or tab. Attribute lists (`[...]`) and backslashes in the path are rejected with a parse error.

### Header recognition

A line is treated as an inline comment header only when it matches one of the patterns above exactly. Other H2-style lines (for example `## Notes` or `## foo:bar`) become part of the surrounding comment body. To put a literal header-shaped line inside a comment, indent it, escape the `#`, or otherwise change the leading characters so it stops matching.

As an exception, any line beginning with `## file:` is assumed to be intended as a file-level header. If it does not match the exact `## file: <path>` pattern (for example `## file:foo.go` with no whitespace, or `## file:` with no path), it is rejected with a parse error rather than treated as body text. This is to catch typos rather than silently swallow them.

### Comment body

Everything after the header up to the next inline comment header (or end of file) is that comment's body. Leading and trailing blank lines are trimmed. Empty bodies are rejected.

## CLI

```
gh-prr submit -f review.md [--pending] [pr_number]
```

Flags must precede the optional `pr_number` positional argument.

- `-f, --file` — path to the Markdown file. Use `-` to read from standard input. Required.
- `--pending` — submit as a pending (draft) review. The `event` from front matter is ignored.

### Submission flow

When the file contains only line-anchored comments and a body, the review is created in a single REST request (`POST /repos/{owner}/{repo}/pulls/{n}/reviews`).

When the file also contains one or more file-level comments (`## file: <path>`), the GitHub REST endpoint cannot send them in the same request. `gh-prr submit` then falls back to a multi-step flow:

1. Create the review as **pending** with the body and any line-anchored comments.
2. Attach each file-level comment via the GraphQL `addPullRequestReviewThread` mutation with `subjectType: FILE`.
3. If `--pending` was not passed, finalize the review with the requested `event`.

If a step after (1) fails, the partially built review is left in pending state — re-run with `gh-prr submit-pending` to finish it, or delete it via the GitHub UI.

```
gh-prr submit-pending [-e EVENT] [pr_number]
```

Submits the current user's existing pending review on the PR. Flags must precede the optional `pr_number`.

- `-e, --event` — `APPROVE`, `REQUEST_CHANGES`, or `COMMENT`. Default `COMMENT`.

## Round-trip with `pending`

`gh-prr pending [pr_number]` prints a Markdown view of the current pending review. Its inline comment headers use the same `## path:line` syntax as `submit`, so a pending review can be exported, edited, and re-submitted by hand. Two things to do before passing the output back to `submit`:

- Prepend a `---` front matter block (the `pending` output does not include one).
- Remove the ` ```diff ` blocks under each header — `pending` includes them as context, but `submit` treats everything after the header as the comment body, so leaving them in re-posts the diff hunk as part of the comment.

## Examples

### A simple comment review with two inline comments

```markdown
---
event: COMMENT
---

Looks good overall. Two small notes below.

## cmd/gh-prr/main.go:120

Could be simplified by returning early here.

## docs/REVIEW_FORMAT.md:1-3

A short overview right under the title would help readers.
```

### Approve without inline comments

```markdown
---
event: APPROVE
---

No issues found. Approving.
```

### Pending review (drafted, not yet submitted)

```bash
gh-prr submit -f review.md --pending
# ...edit further or eyeball it via:
gh-prr pending
# then submit:
gh-prr submit-pending -e COMMENT
```
