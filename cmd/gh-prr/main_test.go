package main

import (
	"encoding/json"
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

func TestParseReviewMarkdown_FrontMatterDuplicateKey(t *testing.T) {
	in := "---\nevent: APPROVE\nevent: COMMENT\n---\nbody\n"
	_, err := parseReviewMarkdown(in)
	if err == nil {
		t.Fatal("expected error for duplicate front matter key")
	}
	if !strings.Contains(err.Error(), "duplicate front matter key") {
		t.Errorf("unexpected error message: %v", err)
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

func TestParseReviewMarkdown_FileLevelRejectsAttributes(t *testing.T) {
	in := "## file: foo.go [side=LEFT]\nbody\n"
	_, err := parseReviewMarkdown(in)
	if err == nil {
		t.Fatal("expected error for attribute list on file-level header")
	}
	if !strings.Contains(err.Error(), "attribute lists are not allowed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseReviewMarkdown_FileLevelRejectsBackslash(t *testing.T) {
	in := "## file: foo\\bar.go\nbody\n"
	_, err := parseReviewMarkdown(in)
	if err == nil {
		t.Fatal("expected error for backslash in file-level path")
	}
	if !strings.Contains(err.Error(), "backslashes are not supported") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseReviewMarkdown_FrontMatterRejectsCommitIDAlias(t *testing.T) {
	in := "---\ncommit_id: deadbeef\n---\nbody\n"
	if _, err := parseReviewMarkdown(in); err == nil {
		t.Fatal("expected error for unknown front matter key commit_id")
	}
}

func TestParseReviewMarkdown_FrontMatterCommitOnly(t *testing.T) {
	in := strings.Join([]string{
		"---",
		"commit: abc123",
		"---",
		"body",
	}, "\n")
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Event != "COMMENT" {
		t.Errorf("event=%q want COMMENT (default)", sub.Event)
	}
	if sub.CommitID != "abc123" {
		t.Errorf("commit=%q want abc123", sub.CommitID)
	}
}

func TestParseReviewMarkdown_FrontMatterEmpty(t *testing.T) {
	in := "---\n---\nbody\n"
	sub, err := parseReviewMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Event != "COMMENT" {
		t.Errorf("event=%q want COMMENT (default)", sub.Event)
	}
	if sub.Body != "body" {
		t.Errorf("body=%q want body", sub.Body)
	}
}

func TestParseReviewMarkdown_FrontMatterEmptyEvent(t *testing.T) {
	in := "---\nevent:\n---\nbody\n"
	if _, err := parseReviewMarkdown(in); err == nil {
		t.Fatal("expected error for empty event value")
	}
}

func TestParseReviewMarkdown_LineAnchoredRejectsBackslash(t *testing.T) {
	in := "## a\\b\\c.go:42\nbody\n"
	_, err := parseReviewMarkdown(in)
	if err == nil {
		t.Fatal("expected error for backslash in line-anchored path")
	}
	if !strings.Contains(err.Error(), "backslashes are not supported") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSubmitReviewRequestJSON_BodyAlwaysIncluded(t *testing.T) {
	// Even when the review has only file-level comments and an empty body,
	// the REST POST must include "body" so the request isn't an empty {}.
	req := submitReviewRequestJSON{}
	blob, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	got := string(blob)
	if !strings.Contains(got, `"body":""`) {
		t.Errorf("expected body field in JSON: %s", got)
	}
}

func TestSubmitReviewRequestJSON_FullRequest(t *testing.T) {
	startLine := 10
	req := submitReviewRequestJSON{
		CommitID: "deadbeef",
		Body:     "summary",
		Event:    "APPROVE",
		Comments: []submitReviewCommentJSON{
			{
				Path:      "foo.go",
				Line:      15,
				Body:      "comment",
				Side:      "RIGHT",
				StartLine: &startLine,
				StartSide: "LEFT",
			},
		},
	}
	blob, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(blob, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip["commit_id"] != "deadbeef" {
		t.Errorf("commit_id=%v", roundTrip["commit_id"])
	}
	if roundTrip["body"] != "summary" {
		t.Errorf("body=%v", roundTrip["body"])
	}
	if roundTrip["event"] != "APPROVE" {
		t.Errorf("event=%v", roundTrip["event"])
	}
	comments, ok := roundTrip["comments"].([]any)
	if !ok || len(comments) != 1 {
		t.Fatalf("comments=%v", roundTrip["comments"])
	}
	c := comments[0].(map[string]any)
	if c["path"] != "foo.go" || c["start_line"].(float64) != 10 || c["start_side"] != "LEFT" {
		t.Errorf("comment=%v", c)
	}
}

func TestRenderPendingMarkdown_FileLevelHeader(t *testing.T) {
	pr := pullRequest{Number: 1, Title: "t", URL: "https://example/pr/1"}
	line5 := 5
	review := &pendingReview{
		ID: "rev1",
		Comments: commentConnection{
			TotalCount: 2,
			Nodes: []comment{
				{
					Path:      "docs/note.md",
					Body:      "whole-file feedback",
					CreatedAt: "2024-01-01T00:00:00Z",
				},
				{
					Path:      "foo.go",
					Line:      &line5,
					Body:      "line feedback",
					CreatedAt: "2024-01-02T00:00:00Z",
				},
			},
		},
	}
	got := renderPendingMarkdown(pr, review, 3)
	if !strings.Contains(got, "## file: docs/note.md\n") {
		t.Errorf("missing file-level header in:\n%s", got)
	}
	if !strings.Contains(got, "## foo.go:5\n") {
		t.Errorf("missing line header in:\n%s", got)
	}
	// The bare path form must NOT be emitted; that broke round-trip into submit.
	if strings.Contains(got, "## docs/note.md\n") {
		t.Errorf("file-level header should not be emitted as bare path:\n%s", got)
	}
}

func TestIsFileLevelComment(t *testing.T) {
	one := 1
	cases := []struct {
		name string
		c    comment
		want bool
	}{
		{"all nil", comment{Path: "f"}, true},
		{"line set", comment{Path: "f", Line: &one}, false},
		{"start_line set", comment{Path: "f", StartLine: &one}, false},
		{"original_line set", comment{Path: "f", OriginalLine: &one}, false},
		{"original_start_line set", comment{Path: "f", OriginalStartLine: &one}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFileLevelComment(tc.c); got != tc.want {
				t.Errorf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestMatchPathGlob(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"foo.go", "foo.go", true},
		{"foo.go", "bar.go", false},
		{"*.go", "main.go", true},
		{"*.go", "pkg/main.go", false},
		{"*.go", "main.js", false},
		{"a/b.go", "a/b.go", true},
		{"a/b.go", "a/c.go", false},
		{"a/*.go", "a/b.go", true},
		{"a/*.go", "a/b/c.go", false},
		{"**/*.go", "main.go", true},
		{"**/*.go", "a/main.go", true},
		{"**/*.go", "a/b/main.go", true},
		{"**/*.go", "main.js", false},
		{"testdata/**", "testdata", true},
		{"testdata/**", "testdata/foo.txt", true},
		{"testdata/**", "testdata/a/b/c.txt", true},
		{"testdata/**", "other/foo.txt", false},
		{"**/testdata/**", "a/testdata/b.txt", true},
		{"**/testdata/**", "src/pkg/testdata/fixtures/x.json", true},
		{"**/testdata/**", "src/data/foo.txt", false},
		{"**/*_test.go", "foo_test.go", true},
		{"**/*_test.go", "pkg/foo_test.go", true},
		{"**/*_test.go", "pkg/foo.go", false},
		{"?.go", "a.go", true},
		{"?.go", "ab.go", false},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/x/c", false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"_"+tc.name, func(t *testing.T) {
			got, err := matchPathGlob(tc.pattern, tc.name)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Errorf("matchPathGlob(%q, %q)=%v want %v", tc.pattern, tc.name, got, tc.want)
			}
		})
	}
}

func TestValidateGlobPattern(t *testing.T) {
	good := []string{
		"foo.go",
		"*.go",
		"**/*.go",
		"testdata/**",
		"a/**/b.go",
		"?.go",
	}
	for _, p := range good {
		if err := validateGlobPattern(p); err != nil {
			t.Errorf("validateGlobPattern(%q) returned err: %v", p, err)
		}
	}

	bad := []string{
		"",
		"foo//bar",
		"a/[",
	}
	for _, p := range bad {
		if err := validateGlobPattern(p); err == nil {
			t.Errorf("validateGlobPattern(%q) expected error, got nil", p)
		}
	}
}

func TestIsAlreadyTarget(t *testing.T) {
	cases := []struct {
		state  string
		unmark bool
		want   bool
	}{
		{"VIEWED", false, true},
		{"UNVIEWED", false, false},
		{"DISMISSED", false, false},
		{"VIEWED", true, false},
		{"UNVIEWED", true, true},
		{"DISMISSED", true, true},
	}
	for _, tc := range cases {
		if got := isAlreadyTarget(tc.state, tc.unmark); got != tc.want {
			t.Errorf("isAlreadyTarget(%q, unmark=%v)=%v want %v", tc.state, tc.unmark, got, tc.want)
		}
	}
}

func TestParseViewedPositional(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantPats []string
		wantPR   *int
		wantErr  bool
	}{
		{"empty", []string{}, nil, nil, false},
		{"single pattern", []string{"*.go"}, []string{"*.go"}, nil, false},
		{"pattern + pr", []string{"*.go", "123"}, []string{"*.go"}, intPtr(123), false},
		{"multi patterns", []string{"a/**", "b/**"}, []string{"a/**", "b/**"}, nil, false},
		{"multi patterns + pr", []string{"a/**", "b/**", "42"}, []string{"a/**", "b/**"}, intPtr(42), false},
		{"numeric pattern alone", []string{"42"}, []string{"42"}, nil, false},
		{"empty pattern errors", []string{"", "42"}, nil, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPats, gotPR, err := parseViewedPositional(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if len(gotPats) != len(tc.wantPats) {
				t.Fatalf("patterns=%v want %v", gotPats, tc.wantPats)
			}
			for i := range gotPats {
				if gotPats[i] != tc.wantPats[i] {
					t.Errorf("patterns[%d]=%q want %q", i, gotPats[i], tc.wantPats[i])
				}
			}
			if (gotPR == nil) != (tc.wantPR == nil) {
				t.Fatalf("prNumber=%v want %v", gotPR, tc.wantPR)
			}
			if gotPR != nil && *gotPR != *tc.wantPR {
				t.Errorf("prNumber=%d want %d", *gotPR, *tc.wantPR)
			}
		})
	}
}

func TestParseInlineHeader_Cases(t *testing.T) {
	cases := []struct {
		header   string
		isHeader bool
		path     string
		lineNum  int
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
		t.Run(tc.header, func(t *testing.T) {
			c, err := parseInlineHeader(tc.header)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if (c != nil) != tc.isHeader {
				t.Fatalf("isHeader=%v want %v", c != nil, tc.isHeader)
			}
			if c == nil {
				return
			}
			if c.Path != tc.path || c.Line != tc.lineNum || c.Side != tc.side {
				t.Errorf("got=%+v", c)
			}
			if (c.StartLine == nil) != (tc.startPtr == nil) || (c.StartLine != nil && *c.StartLine != *tc.startPtr) {
				t.Errorf("start=%v want %v", c.StartLine, tc.startPtr)
			}
		})
	}
}
