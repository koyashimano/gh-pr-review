package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	inlineHeaderRe   = regexp.MustCompile(`^## ([^:]+):(\d+)(?:-(\d+))?(?:[ \t]+\[([^\]]*)\])?[ \t]*$`)
	fileHeaderRe     = regexp.MustCompile(`^## file:[ \t]+(.+?)[ \t]*$`)
	fileHeaderPrefix = "## file:"
)

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

func splitFrontMatter(content string) (matter, rest string, err error) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return "", content, nil
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			matter = strings.Join(lines[1:i], "\n")
			rest = strings.Join(lines[i+1:], "\n")
			return matter, rest, nil
		}
	}
	return "", "", errors.New("front matter opened with --- but never closed with ---")
}

func stripQuotes(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func parseFrontMatter(matter string, sub *reviewSubmission) error {
	seen := map[string]bool{}
	for _, raw := range strings.Split(matter, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			return fmt.Errorf("invalid front matter line %q (expected key: value)", raw)
		}
		key := strings.TrimSpace(line[:idx])
		val := stripQuotes(strings.TrimSpace(line[idx+1:]))
		if seen[key] {
			return fmt.Errorf("duplicate front matter key %q", key)
		}
		seen[key] = true
		switch key {
		case "event":
			v := strings.ToUpper(val)
			switch v {
			case "APPROVE", "REQUEST_CHANGES", "COMMENT":
				sub.Event = v
			default:
				return fmt.Errorf("invalid event %q (expected APPROVE, REQUEST_CHANGES, or COMMENT)", val)
			}
		case "commit":
			sub.CommitID = val
		default:
			return fmt.Errorf("unknown front matter key %q", key)
		}
	}
	return nil
}

func parseInlineHeaderAttrs(attrs string, c *reviewComment) error {
	if strings.TrimSpace(attrs) == "" {
		return nil
	}
	seen := map[string]bool{}
	for _, part := range strings.Split(attrs, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.Index(part, "=")
		if eq < 0 {
			return fmt.Errorf("invalid attribute %q (expected key=value)", part)
		}
		key := strings.TrimSpace(part[:eq])
		val := strings.TrimSpace(part[eq+1:])
		if seen[key] {
			return fmt.Errorf("duplicate attribute %q", key)
		}
		seen[key] = true
		switch key {
		case "side":
			v := strings.ToUpper(val)
			if v != "LEFT" && v != "RIGHT" {
				return fmt.Errorf("invalid side %q (expected LEFT or RIGHT)", val)
			}
			c.Side = v
		case "start_side":
			v := strings.ToUpper(val)
			if v != "LEFT" && v != "RIGHT" {
				return fmt.Errorf("invalid start_side %q (expected LEFT or RIGHT)", val)
			}
			c.StartSide = v
		default:
			return fmt.Errorf("unknown attribute %q", key)
		}
	}
	return nil
}

func parseInlineHeader(line string) (*reviewComment, error) {
	if strings.HasPrefix(line, fileHeaderPrefix) {
		fm := fileHeaderRe.FindStringSubmatch(line)
		if fm == nil {
			return nil, fmt.Errorf("malformed file-level header %q (expected `## file: <path>`)", line)
		}
		path := strings.TrimSpace(fm[1])
		if path == "" {
			return nil, fmt.Errorf("missing path in header %q", line)
		}
		if strings.ContainsAny(path, "[]") {
			return nil, fmt.Errorf("attribute lists are not allowed on file-level headers (%q)", line)
		}
		if strings.Contains(path, `\`) {
			return nil, fmt.Errorf("backslashes are not supported in path %q (use forward slashes)", path)
		}
		return &reviewComment{Path: path, SubjectFile: true}, nil
	}

	m := inlineHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return nil, nil
	}

	path := strings.TrimSpace(m[1])
	if path == "" {
		return nil, nil
	}
	if strings.Contains(path, `\`) {
		return nil, fmt.Errorf("backslashes are not supported in path %q (use forward slashes)", path)
	}

	first, err := strconv.Atoi(m[2])
	if err != nil || first < 1 {
		return nil, fmt.Errorf("invalid line number in header %q", line)
	}

	c := &reviewComment{Path: path, Line: first}

	if m[3] != "" {
		end, err := strconv.Atoi(m[3])
		if err != nil || end < 1 {
			return nil, fmt.Errorf("invalid end line in header %q", line)
		}
		if first >= end {
			return nil, fmt.Errorf("start line %d must be less than end line %d in header %q", first, end, line)
		}
		start := first
		c.StartLine = &start
		c.Line = end
	}

	if m[4] != "" {
		if err := parseInlineHeaderAttrs(m[4], c); err != nil {
			return nil, fmt.Errorf("%w in header %q", err, line)
		}
	}

	if c.StartLine == nil && c.StartSide != "" {
		return nil, fmt.Errorf("start_side specified without a line range in header %q", line)
	}

	return c, nil
}

func trimBlankLines(lines []string) string {
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}

func parseReviewMarkdown(content string) (reviewSubmission, error) {
	sub := reviewSubmission{Event: "COMMENT"}

	content = normalizeNewlines(content)

	matter, rest, err := splitFrontMatter(content)
	if err != nil {
		return sub, err
	}
	if matter != "" {
		if err := parseFrontMatter(matter, &sub); err != nil {
			return sub, err
		}
	}

	lines := strings.Split(rest, "\n")

	var bodyLines []string
	var current *reviewComment
	var currentLines []string

	flushCurrent := func() error {
		body := trimBlankLines(currentLines)
		if body == "" {
			var loc string
			if current.SubjectFile {
				loc = fmt.Sprintf("file: %s", current.Path)
			} else {
				loc = fmt.Sprintf("%s:%d", current.Path, current.Line)
			}
			return fmt.Errorf("review comment for %s has empty body", loc)
		}
		current.Body = body
		sub.Comments = append(sub.Comments, *current)
		current = nil
		currentLines = nil
		return nil
	}

	for _, line := range lines {
		c, err := parseInlineHeader(line)
		if err != nil {
			return sub, err
		}
		if c != nil {
			if current != nil {
				if err := flushCurrent(); err != nil {
					return sub, err
				}
			}
			current = c
			continue
		}
		if current != nil {
			currentLines = append(currentLines, line)
		} else {
			bodyLines = append(bodyLines, line)
		}
	}

	sub.Body = trimBlankLines(bodyLines)

	if current != nil {
		if err := flushCurrent(); err != nil {
			return sub, err
		}
	}

	return sub, nil
}

func validateReviewSubmission(sub reviewSubmission, finalize bool) error {
	if !finalize {
		return nil
	}
	switch sub.Event {
	case "APPROVE":
		return nil
	case "COMMENT", "REQUEST_CHANGES":
		if strings.TrimSpace(sub.Body) == "" && len(sub.Comments) == 0 {
			return fmt.Errorf("event %s requires a body or at least one inline comment", sub.Event)
		}
		return nil
	default:
		return fmt.Errorf("invalid event %q", sub.Event)
	}
}
