package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// prFileDiff describes a single file in the PR's diff for the purpose of
// validating inline comment locations before they are sent to GitHub.
type prFileDiff struct {
	path             string
	previousFilename string
	patch            string
	// leftLines is the set of line numbers on the pre-change ("LEFT") side
	// where a review comment can be anchored (context + deleted lines).
	leftLines map[int]bool
	// rightLines is the set of line numbers on the post-change ("RIGHT")
	// side where a review comment can be anchored (context + added lines).
	rightLines map[int]bool
}

type restPRFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Patch            string `json:"patch"`
}

func fetchPRDiffs(owner, repo string, prNumber int) (map[string]*prFileDiff, error) {
	cmd := []string{
		"gh", "api",
		"--paginate",
		"-H", "Accept: application/vnd.github+json",
		fmt.Sprintf("/repos/%s/%s/pulls/%d/files?per_page=100", owner, repo, prNumber),
	}
	out, err := run(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch PR files for validation: %w", err)
	}
	files, err := decodePaginatedFiles(out)
	if err != nil {
		return nil, fmt.Errorf("failed to parse PR files JSON: %w", err)
	}

	diffs := make(map[string]*prFileDiff, len(files))
	for _, f := range files {
		d := &prFileDiff{
			path:             f.Filename,
			previousFilename: f.PreviousFilename,
			patch:            f.Patch,
		}
		if f.Patch != "" {
			left, right, err := parsePatchCommentableLines(f.Patch)
			if err != nil {
				return nil, fmt.Errorf("failed to parse patch for %s: %w", f.Filename, err)
			}
			d.leftLines = left
			d.rightLines = right
		}
		diffs[f.Filename] = d
	}
	return diffs, nil
}

// decodePaginatedFiles parses the output of `gh api --paginate` for a JSON
// array endpoint. Modern gh versions merge pages into a single JSON array,
// but older versions concatenate arrays without merging; the decoder loop
// handles both shapes.
func decodePaginatedFiles(out string) ([]restPRFile, error) {
	dec := json.NewDecoder(strings.NewReader(out))
	var all []restPRFile
	for dec.More() {
		var page []restPRFile
		if err := dec.Decode(&page); err != nil {
			return nil, err
		}
		all = append(all, page...)
	}
	return all, nil
}

var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// parsePatchCommentableLines walks a unified diff (just the hunks, as
// returned by GitHub's REST `pulls/{n}/files` endpoint) and reports which
// line numbers are commentable on each side. A line is commentable when it
// appears in the diff — context and deleted lines on LEFT, context and
// added lines on RIGHT.
func parsePatchCommentableLines(patch string) (left, right map[int]bool, err error) {
	left = map[int]bool{}
	right = map[int]bool{}

	var leftLine, rightLine int
	inHunk := false
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@") {
			m := hunkHeaderRe.FindStringSubmatch(line)
			if m == nil {
				return nil, nil, fmt.Errorf("malformed hunk header: %q", line)
			}
			leftLine, _ = strconv.Atoi(m[1])
			rightLine, _ = strconv.Atoi(m[3])
			inHunk = true
			continue
		}
		if !inHunk || line == "" {
			continue
		}
		switch line[0] {
		case ' ':
			left[leftLine] = true
			right[rightLine] = true
			leftLine++
			rightLine++
		case '-':
			left[leftLine] = true
			leftLine++
		case '+':
			right[rightLine] = true
			rightLine++
		case '\\':
			// "\ No newline at end of file" — no line consumed.
		default:
			inHunk = false
		}
	}
	return left, right, nil
}

func validateCommentsAgainstDiff(sub reviewSubmission, diffs map[string]*prFileDiff) error {
	var msgs []string
	for _, c := range sub.Comments {
		if err := validateOneComment(c, diffs); err != nil {
			msgs = append(msgs, err.Error())
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	if len(msgs) == 1 {
		return errors.New(msgs[0])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d invalid review comment(s):", len(msgs))
	for _, m := range msgs {
		b.WriteString("\n  - ")
		b.WriteString(m)
	}
	return errors.New(b.String())
}

func validateOneComment(c reviewComment, diffs map[string]*prFileDiff) error {
	d, ok := diffs[c.Path]
	if !ok {
		for _, dd := range diffs {
			if dd.previousFilename != "" && dd.previousFilename == c.Path {
				return fmt.Errorf("file %q is not in the PR's changed files; it appears to have been renamed to %q — use the new path", c.Path, dd.path)
			}
		}
		return fmt.Errorf("file %q is not in the PR's changed files", c.Path)
	}
	if c.SubjectFile {
		return nil
	}
	if d.patch == "" {
		// Binary file or a patch that GitHub omitted (e.g. too large).
		// We cannot tell which line numbers are valid, so leave it to
		// the server.
		return nil
	}

	side := c.Side
	if side == "" {
		side = "RIGHT"
	}
	if err := checkCommentableLine(d, c.Line, side); err != nil {
		return fmt.Errorf("%s:%d: %w", c.Path, c.Line, err)
	}
	if c.StartLine != nil {
		startSide := c.StartSide
		if startSide == "" {
			startSide = side
		}
		if err := checkCommentableLine(d, *c.StartLine, startSide); err != nil {
			return fmt.Errorf("%s:%d (range start): %w", c.Path, *c.StartLine, err)
		}
	}
	return nil
}

func checkCommentableLine(d *prFileDiff, line int, side string) error {
	var lines map[int]bool
	switch side {
	case "LEFT":
		lines = d.leftLines
	case "RIGHT":
		lines = d.rightLines
	default:
		return fmt.Errorf("invalid side %q", side)
	}
	if lines[line] {
		return nil
	}
	if len(lines) == 0 {
		return fmt.Errorf("side=%s is not in the diff (no %s-side lines exist for this file — try the other side)", side, side)
	}
	return fmt.Errorf("side=%s is not in the diff (only lines shown in the hunks of this PR can be commented on)", side)
}
