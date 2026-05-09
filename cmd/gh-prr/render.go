package main

import (
	"fmt"
	"sort"
	"strings"
)

func abbreviateDiffHunk(diffHunk string, ctx int) string {
	if ctx < 1 {
		ctx = 1
	}
	normalizedDiff := strings.ReplaceAll(strings.ReplaceAll(diffHunk, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(normalizedDiff, "\n")

	if len(lines) <= ctx*2 {
		return strings.Join(lines, "\n")
	}

	head := strings.Join(lines[:ctx], "\n")
	tail := strings.Join(lines[len(lines)-ctx:], "\n")
	return head + "\n…\n" + tail
}

func isFileLevelComment(c comment) bool {
	return c.Line == nil && c.StartLine == nil && c.OriginalLine == nil && c.OriginalStartLine == nil
}

func fmtLoc(path string, startLine, line *int) string {
	switch {
	case startLine != nil && line != nil && *startLine != *line:
		return fmt.Sprintf("%s:%d-%d", path, *startLine, *line)
	case line != nil:
		return fmt.Sprintf("%s:%d", path, *line)
	case startLine != nil:
		return fmt.Sprintf("%s:%d", path, *startLine)
	default:
		return path
	}
}

func threadSortKey(t reviewThread) string {
	if len(t.Comments.Nodes) > 0 {
		if ts := t.Comments.Nodes[0].CreatedAt; ts != "" {
			return ts
		}
	}
	return ""
}

func renderMarkdown(pr pullRequest, threads []reviewThread, ctx int, unresolvedOnly bool) string {
	var out []string

	out = append(out, "# PR Review", "")
	out = append(out, fmt.Sprintf("- PR: %s", pr.URL))
	out = append(out, fmt.Sprintf("- Title: %s", pr.Title))
	out = append(out, fmt.Sprintf("- Number: %d", pr.Number))
	out = append(out, "")

	sorted := make([]reviewThread, len(threads))
	copy(sorted, threads)
	sort.SliceStable(sorted, func(i, j int) bool {
		return threadSortKey(sorted[i]) < threadSortKey(sorted[j])
	})

	for _, thread := range sorted {
		if unresolvedOnly && thread.IsResolved {
			continue
		}

		path := thread.Path
		if path == "" {
			path = "?"
		}
		loc := fmtLoc(path, thread.StartLine, thread.Line)

		status := "Unresolved"
		if thread.IsResolved {
			status = "Resolved"
		}

		statusLine := fmt.Sprintf("**Status:** %s", status)
		if thread.IsResolved && thread.ResolvedBy != nil && thread.ResolvedBy.Login != "" {
			statusLine += fmt.Sprintf(" (by %s)", thread.ResolvedBy.Login)
		}

		side := thread.DiffSide
		if side == "" {
			side = "RIGHT"
		}
		sideLine := fmt.Sprintf("- Side: %s", side)
		if thread.StartDiffSide != "" {
			sideLine += fmt.Sprintf(" (start: %s)", thread.StartDiffSide)
		}

		out = append(out, fmt.Sprintf("## %s", loc))
		out = append(out, statusLine)
		out = append(out, sideLine)

		if thread.IsOutdated {
			out = append(out, "- Note: Outdated thread")
		}
		if thread.IsCollapsed {
			out = append(out, "- Note: Collapsed thread")
		}
		out = append(out, "")

		comments := thread.Comments.Nodes
		totalCount := thread.Comments.TotalCount

		if len(comments) == 0 {
			out = append(out, "_No comments in this thread._", "")
			continue
		}

		for i, comment := range comments {
			author := "?"
			if comment.Author != nil && comment.Author.Login != "" {
				author = comment.Author.Login
			}
			createdAt := comment.CreatedAt
			url := comment.URL
			body := strings.TrimRight(comment.Body, "\n\r")

			out = append(out, fmt.Sprintf("### %s at %s", author, createdAt))
			if url != "" {
				out = append(out, fmt.Sprintf("- URL: %s", url))
			}
			out = append(out, "")
			if i == 0 && strings.TrimSpace(comment.DiffHunk) != "" {
				diffBlock := abbreviateDiffHunk(comment.DiffHunk, ctx)
				out = append(out, "```diff")
				out = append(out, diffBlock)
				out = append(out, "```")
				out = append(out, "")
			}
			out = append(out, "")
			if body == "" {
				body = "_(empty)_"
			}
			out = append(out, body)
			out = append(out, "")
		}

		if totalCount > len(comments) {
			out = append(out, fmt.Sprintf("> Note: comments truncated (%d/%d).", len(comments), totalCount))
			out = append(out, "")
		}
	}

	return strings.Join(out, "\n") + "\n"
}

func renderPendingMarkdown(pr pullRequest, review *pendingReview, ctx int) string {
	var out []string

	out = append(out, "# Pending Review Comments", "")
	out = append(out, fmt.Sprintf("- PR: %s", pr.URL))
	out = append(out, fmt.Sprintf("- Title: %s", pr.Title))
	out = append(out, fmt.Sprintf("- Number: %d", pr.Number))

	if review == nil || len(review.Comments.Nodes) == 0 {
		out = append(out, "")
		out = append(out, "No pending review comments.")
		out = append(out, "")
		return strings.Join(out, "\n") + "\n"
	}

	out = append(out, fmt.Sprintf("- Pending comments: %d", len(review.Comments.Nodes)))

	if body := strings.TrimSpace(review.Body); body != "" {
		out = append(out, "")
		out = append(out, "## Review Body")
		out = append(out, "")
		out = append(out, body)
	}

	out = append(out, "")

	comments := make([]comment, len(review.Comments.Nodes))
	copy(comments, review.Comments.Nodes)
	sort.SliceStable(comments, func(i, j int) bool {
		return comments[i].CreatedAt < comments[j].CreatedAt
	})

	for _, c := range comments {
		var header string
		if isFileLevelComment(c) {
			header = fmt.Sprintf("## file: %s", c.Path)
		} else {
			header = fmt.Sprintf("## %s", fmtLoc(c.Path, c.StartLine, c.Line))
		}
		out = append(out, header)

		out = append(out, "")
		if strings.TrimSpace(c.DiffHunk) != "" {
			diffBlock := abbreviateDiffHunk(c.DiffHunk, ctx)
			out = append(out, "```diff")
			out = append(out, diffBlock)
			out = append(out, "```")
			out = append(out, "")
		}

		body := strings.TrimRight(c.Body, "\n\r")
		if body == "" {
			body = "_(empty)_"
		}
		out = append(out, body)
		out = append(out, "")
	}

	if review.Comments.TotalCount > len(review.Comments.Nodes) {
		out = append(out, fmt.Sprintf("> Note: comments truncated (%d/%d).", len(review.Comments.Nodes), review.Comments.TotalCount))
		out = append(out, "")
	}

	return strings.Join(out, "\n") + "\n"
}
