package main

import (
	"strings"
	"testing"
)

func intPtr(n int) *int { return &n }

func TestParseReviewMarkdown_OnlyBody(t *testing.T) {
	in := "Just a summary.\n\nMore text.\n"
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Event != "COMMENT" {
		t.Errorf("event=%q want COMMENT", sub.Event)
	}
	if sub.Body != "Just a summary.\n\nMore text." {
		t.Errorf("body=%q", sub.Body)
	}
	if len(sub.Comments) != 0 {
		t.Errorf("comments=%d want 0", len(sub.Comments))
	}
}

func TestParseReviewMarkdown_FrontMatter(t *testing.T) {
	in := strings.Join([]string{
		"---",
		"event: APPROVE",
		"commit: abc123",
		"---",
		"",
		"LGTM",
		"",
	}, "\n")
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Event != "APPROVE" {
		t.Errorf("event=%q want APPROVE", sub.Event)
	}
	if sub.CommitID != "abc123" {
		t.Errorf("commit=%q want abc123", sub.CommitID)
	}
	if sub.Body != "LGTM" {
		t.Errorf("body=%q want LGTM", sub.Body)
	}
}

func TestParseReviewMarkdown_FrontMatterQuotedAndComments(t *testing.T) {
	in := strings.Join([]string{
		"---",
		"# this is a comment",
		"event: 'COMMENT'",
		"commit: \"deadbeef\"",
		"---",
		"body",
	}, "\n")
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Event != "COMMENT" {
		t.Errorf("event=%q", sub.Event)
	}
	if sub.CommitID != "deadbeef" {
		t.Errorf("commit=%q", sub.CommitID)
	}
}

func TestParseReviewMarkdown_FrontMatterUnknownKey(t *testing.T) {
	in := "---\nevent: COMMENT\nfoo: bar\n---\nbody\n"
	if _, err := parseReviewMarkdown(in); err == nil {
		t.Fatal("expected error for unknown front matter key")
	}
}

func TestParseReviewMarkdown_FrontMatterInvalidEvent(t *testing.T) {
	in := "---\nevent: PLEASE\n---\nbody\n"
	if _, err := parseReviewMarkdown(in); err == nil {
		t.Fatal("expected error for invalid event")
	}
}

func TestParseReviewMarkdown_FrontMatterUnclosed(t *testing.T) {
	in := "---\nevent: COMMENT\n\nbody\n"
	if _, err := parseReviewMarkdown(in); err == nil {
		t.Fatal("expected error for unclosed front matter")
	}
}

func TestParseReviewMarkdown_InlineSingleLine(t *testing.T) {
	in := strings.Join([]string{
		"Summary.",
		"",
		"## foo/bar.go:42",
		"",
		"Inline body.",
		"",
	}, "\n")
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Body != "Summary." {
		t.Errorf("body=%q", sub.Body)
	}
	if len(sub.Comments) != 1 {
		t.Fatalf("comments=%d want 1", len(sub.Comments))
	}
	c := sub.Comments[0]
	if c.Path != "foo/bar.go" || c.Line != 42 || c.StartLine != nil || c.Side != "" {
		t.Errorf("unexpected comment: %+v", c)
	}
	if c.Body != "Inline body." {
		t.Errorf("comment body=%q", c.Body)
	}
}

func TestParseReviewMarkdown_InlineMultilineRange(t *testing.T) {
	in := strings.Join([]string{
		"## foo.go:10-15",
		"range",
	}, "\n")
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Comments) != 1 {
		t.Fatalf("comments=%d", len(sub.Comments))
	}
	c := sub.Comments[0]
	if c.Path != "foo.go" || c.Line != 15 || c.StartLine == nil || *c.StartLine != 10 {
		t.Errorf("unexpected: %+v (start_line=%v)", c, c.StartLine)
	}
}

func TestParseReviewMarkdown_InlineWithSide(t *testing.T) {
	in := "## foo.go:5 [side=LEFT]\nbody\n"
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Comments) != 1 {
		t.Fatalf("comments=%d", len(sub.Comments))
	}
	if got := sub.Comments[0].Side; got != "LEFT" {
		t.Errorf("side=%q want LEFT", got)
	}
}

func TestParseReviewMarkdown_InlineWithStartSide(t *testing.T) {
	in := "## foo.go:5-10 [side=RIGHT, start_side=LEFT]\nbody\n"
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Comments) != 1 {
		t.Fatalf("comments=%d", len(sub.Comments))
	}
	c := sub.Comments[0]
	if c.Side != "RIGHT" || c.StartSide != "LEFT" {
		t.Errorf("side=%q start_side=%q", c.Side, c.StartSide)
	}
}

func TestParseReviewMarkdown_InvalidStartLine(t *testing.T) {
	in := "## foo.go:10-5\nbody\n"
	if _, err := parseReviewMarkdown(in); err == nil {
		t.Fatal("expected error for start_line >= line")
	}
}

func TestParseReviewMarkdown_StartSideWithoutRange(t *testing.T) {
	in := "## foo.go:5 [start_side=LEFT]\nbody\n"
	if _, err := parseReviewMarkdown(in); err == nil {
		t.Fatal("expected error for start_side without range")
	}
}

func TestParseReviewMarkdown_InvalidSide(t *testing.T) {
	in := "## foo.go:5 [side=BACK]\nbody\n"
	if _, err := parseReviewMarkdown(in); err == nil {
		t.Fatal("expected error for invalid side")
	}
}

func TestParseReviewMarkdown_UnknownAttr(t *testing.T) {
	in := "## foo.go:5 [color=red]\nbody\n"
	if _, err := parseReviewMarkdown(in); err == nil {
		t.Fatal("expected error for unknown attr")
	}
}

func TestParseReviewMarkdown_EmptyInlineBody(t *testing.T) {
	in := "## foo.go:5\n\n## foo.go:6\nactual body\n"
	if _, err := parseReviewMarkdown(in); err == nil {
		t.Fatal("expected error for empty inline body")
	}
}

func TestParseReviewMarkdown_HeaderInsideBodyDoesntMatch(t *testing.T) {
	// "## foo:bar" is not a valid header (bar is not a number)
	in := "Summary.\n\n## foo:bar baz\n\n## foo.go:1\ninline\n"
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sub.Body, "## foo:bar baz") {
		t.Errorf("body lost the literal header line: %q", sub.Body)
	}
	if len(sub.Comments) != 1 {
		t.Fatalf("comments=%d", len(sub.Comments))
	}
}

func TestParseReviewMarkdown_MultipleComments(t *testing.T) {
	in := strings.Join([]string{
		"---",
		"event: REQUEST_CHANGES",
		"---",
		"",
		"Summary line.",
		"",
		"## a.go:1",
		"comment A",
		"",
		"## b.go:10-12 [side=RIGHT]",
		"comment B",
		"",
	}, "\n")
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Event != "REQUEST_CHANGES" {
		t.Errorf("event=%q", sub.Event)
	}
	if sub.Body != "Summary line." {
		t.Errorf("body=%q", sub.Body)
	}
	if len(sub.Comments) != 2 {
		t.Fatalf("comments=%d", len(sub.Comments))
	}
	if sub.Comments[0].Path != "a.go" || sub.Comments[0].Line != 1 {
		t.Errorf("c0=%+v", sub.Comments[0])
	}
	if sub.Comments[0].Body != "comment A" {
		t.Errorf("c0 body=%q", sub.Comments[0].Body)
	}
	if sub.Comments[1].Path != "b.go" || sub.Comments[1].Line != 12 {
		t.Errorf("c1=%+v", sub.Comments[1])
	}
	if sub.Comments[1].StartLine == nil || *sub.Comments[1].StartLine != 10 {
		t.Errorf("c1 start_line=%v", sub.Comments[1].StartLine)
	}
	if sub.Comments[1].Side != "RIGHT" {
		t.Errorf("c1 side=%q", sub.Comments[1].Side)
	}
	if sub.Comments[1].Body != "comment B" {
		t.Errorf("c1 body=%q", sub.Comments[1].Body)
	}
}

func TestParseReviewMarkdown_PreservesInternalBlankLines(t *testing.T) {
	in := strings.Join([]string{
		"## foo.go:1",
		"",
		"line1",
		"",
		"line2",
		"",
	}, "\n")
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Comments) != 1 {
		t.Fatalf("comments=%d", len(sub.Comments))
	}
	want := "line1\n\nline2"
	if sub.Comments[0].Body != want {
		t.Errorf("body=%q want %q", sub.Comments[0].Body, want)
	}
}

func TestParseReviewMarkdown_CRLF(t *testing.T) {
	in := "---\r\nevent: COMMENT\r\n---\r\nbody\r\n\r\n## foo.go:1\r\ninline\r\n"
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Body != "body" {
		t.Errorf("body=%q", sub.Body)
	}
	if len(sub.Comments) != 1 || sub.Comments[0].Body != "inline" {
		t.Errorf("comments=%+v", sub.Comments)
	}
}

func TestValidateReviewSubmission(t *testing.T) {
	cases := []struct {
		name    string
		sub     reviewSubmission
		pending bool
		wantErr bool
	}{
		{"pending allows empty", reviewSubmission{Event: "COMMENT"}, true, false},
		{"approve allows empty", reviewSubmission{Event: "APPROVE"}, false, false},
		{"comment empty fails", reviewSubmission{Event: "COMMENT"}, false, true},
		{"comment with body ok", reviewSubmission{Event: "COMMENT", Body: "ok"}, false, false},
		{"comment with comments ok", reviewSubmission{Event: "COMMENT", Comments: []reviewComment{{Path: "a", Line: 1, Body: "x"}}}, false, false},
		{"request_changes empty fails", reviewSubmission{Event: "REQUEST_CHANGES"}, false, true},
		{"unknown event fails", reviewSubmission{Event: "FOO"}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateReviewSubmission(tc.sub, tc.pending)
			if (err != nil) != tc.wantErr {
				t.Errorf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestSplitFrontMatter_NoFrontMatter(t *testing.T) {
	matter, rest, err := splitFrontMatter("just body\n")
	if err != nil {
		t.Fatal(err)
	}
	if matter != "" {
		t.Errorf("matter=%q want empty", matter)
	}
	if rest != "just body\n" {
		t.Errorf("rest=%q", rest)
	}
}

func TestParseReviewMarkdown_FileLevelComment(t *testing.T) {
	in := strings.Join([]string{
		"Summary.",
		"",
		"## file: docs/README.md",
		"",
		"This whole file needs an overhaul.",
		"",
		"## foo.go:5",
		"line comment",
		"",
	}, "\n")
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Comments) != 2 {
		t.Fatalf("comments=%d", len(sub.Comments))
	}
	if !sub.Comments[0].SubjectFile {
		t.Errorf("c0 expected SubjectFile=true: %+v", sub.Comments[0])
	}
	if sub.Comments[0].Path != "docs/README.md" || sub.Comments[0].Line != 0 || sub.Comments[0].StartLine != nil {
		t.Errorf("c0=%+v", sub.Comments[0])
	}
	if sub.Comments[0].Body != "This whole file needs an overhaul." {
		t.Errorf("c0 body=%q", sub.Comments[0].Body)
	}
	if sub.Comments[1].SubjectFile {
		t.Errorf("c1 should not be SubjectFile: %+v", sub.Comments[1])
	}
	if sub.Comments[1].Path != "foo.go" || sub.Comments[1].Line != 5 {
		t.Errorf("c1=%+v", sub.Comments[1])
	}
}

func TestParseReviewMarkdown_FileLevelEmptyPath(t *testing.T) {
	in := "## file:   \nbody\n"
	if _, err := parseReviewMarkdown(in); err == nil {
		t.Fatal("expected error for malformed file-level header")
	}
}

func TestParseReviewMarkdown_FileLevelMissingSpace(t *testing.T) {
	in := "## file:foo.go\nbody\n"
	if _, err := parseReviewMarkdown(in); err == nil {
		t.Fatal("expected error for malformed file-level header")
	}
}

func TestParseReviewMarkdown_FileLevelEmptyBody(t *testing.T) {
	in := "## file: foo.go\n\n## file: bar.go\nactual\n"
	if _, err := parseReviewMarkdown(in); err == nil {
		t.Fatal("expected error for empty file-level body")
	}
}

func TestParseInlineHeader_Cases(t *testing.T) {
	cases := []struct {
		line     string
		isHeader bool
		path     string
		line_    int
		startPtr *int
		side     string
	}{
		{"## a.go:1", true, "a.go", 1, nil, ""},
		{"## a/b/c.go:42", true, "a/b/c.go", 42, nil, ""},
		{"## a.go:1-3", true, "a.go", 3, intPtr(1), ""},
		{"## a.go:1 [side=LEFT]", true, "a.go", 1, nil, "LEFT"},
		{"# a.go:1", false, "", 0, nil, ""},
		{"##a.go:1", false, "", 0, nil, ""},
		{"## :1", false, "", 0, nil, ""},
		{"## a.go:abc", false, "", 0, nil, ""},
		{"random text", false, "", 0, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			c, ok, err := parseInlineHeader(tc.line)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if ok != tc.isHeader {
				t.Fatalf("ok=%v want %v", ok, tc.isHeader)
			}
			if !ok {
				return
			}
			if c.Path != tc.path || c.Line != tc.line_ || c.Side != tc.side {
				t.Errorf("got=%+v", c)
			}
			if (c.StartLine == nil) != (tc.startPtr == nil) || (c.StartLine != nil && *c.StartLine != *tc.startPtr) {
				t.Errorf("start=%v want %v", c.StartLine, tc.startPtr)
			}
		})
	}
}
