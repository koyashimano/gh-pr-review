package main

import (
	"strings"
	"testing"
)

func TestParsePatchCommentableLines_MixedHunk(t *testing.T) {
	patch := strings.Join([]string{
		"@@ -10,5 +10,6 @@",
		" ctx10",
		" ctx11",
		"-removed12",
		"+added12",
		"+added13",
		" ctx14",
	}, "\n")

	left, right, err := parsePatchCommentableLines(patch)
	if err != nil {
		t.Fatal(err)
	}

	// LEFT: ctx10(10), ctx11(11), removed(12), ctx(13)
	for _, n := range []int{10, 11, 12, 13} {
		if !left[n] {
			t.Errorf("expected LEFT to include line %d, got %v", n, left)
		}
	}
	if left[14] {
		t.Errorf("LEFT should not include line 14: %v", left)
	}

	// RIGHT: ctx10(10), ctx11(11), added(12), added(13), ctx(14)
	for _, n := range []int{10, 11, 12, 13, 14} {
		if !right[n] {
			t.Errorf("expected RIGHT to include line %d, got %v", n, right)
		}
	}
	if right[15] {
		t.Errorf("RIGHT should not include line 15: %v", right)
	}
}

func TestParsePatchCommentableLines_NewFile(t *testing.T) {
	patch := strings.Join([]string{
		"@@ -0,0 +1,3 @@",
		"+first",
		"+second",
		"+third",
	}, "\n")

	left, right, err := parsePatchCommentableLines(patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("LEFT should be empty for new file: %v", left)
	}
	for _, n := range []int{1, 2, 3} {
		if !right[n] {
			t.Errorf("RIGHT missing line %d: %v", n, right)
		}
	}
}

func TestParsePatchCommentableLines_DeletedFile(t *testing.T) {
	patch := strings.Join([]string{
		"@@ -1,2 +0,0 @@",
		"-line1",
		"-line2",
	}, "\n")

	left, right, err := parsePatchCommentableLines(patch)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2} {
		if !left[n] {
			t.Errorf("LEFT missing line %d: %v", n, left)
		}
	}
	if len(right) != 0 {
		t.Errorf("RIGHT should be empty for deleted file: %v", right)
	}
}

func TestParsePatchCommentableLines_MultipleHunks(t *testing.T) {
	patch := strings.Join([]string{
		"@@ -1,2 +1,2 @@",
		" a",
		"-b",
		"+B",
		"@@ -20,2 +20,2 @@",
		" x",
		"-y",
		"+Y",
	}, "\n")

	left, right, err := parsePatchCommentableLines(patch)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2, 20, 21} {
		if !left[n] {
			t.Errorf("LEFT missing %d: %v", n, left)
		}
		if !right[n] {
			t.Errorf("RIGHT missing %d: %v", n, right)
		}
	}
	if left[3] || right[3] || left[22] || right[22] {
		t.Errorf("unexpected commentable line between hunks")
	}
}

func TestParsePatchCommentableLines_NoNewlineMarker(t *testing.T) {
	patch := strings.Join([]string{
		"@@ -1,2 +1,2 @@",
		" a",
		"-b",
		"\\ No newline at end of file",
		"+B",
		"\\ No newline at end of file",
	}, "\n")

	left, right, err := parsePatchCommentableLines(patch)
	if err != nil {
		t.Fatal(err)
	}
	if !left[1] || !left[2] {
		t.Errorf("LEFT missing expected lines: %v", left)
	}
	if !right[1] || !right[2] {
		t.Errorf("RIGHT missing expected lines: %v", right)
	}
}

func TestParsePatchCommentableLines_DefaultHunkCount(t *testing.T) {
	// `@@ -10 +10 @@` (no comma) — counts default to 1.
	patch := strings.Join([]string{
		"@@ -10 +10 @@",
		"-old",
		"+new",
	}, "\n")
	left, right, err := parsePatchCommentableLines(patch)
	if err != nil {
		t.Fatal(err)
	}
	if !left[10] {
		t.Errorf("LEFT missing 10: %v", left)
	}
	if !right[10] {
		t.Errorf("RIGHT missing 10: %v", right)
	}
}

func TestValidateCommentsAgainstDiff_OK(t *testing.T) {
	diffs := map[string]*prFileDiff{
		"foo.go": {
			Path:       "foo.go",
			Patch:      "non-empty",
			leftLines:  map[int]bool{10: true, 11: true},
			rightLines: map[int]bool{10: true, 11: true, 12: true},
		},
	}
	start := 10
	sub := reviewSubmission{
		Comments: []reviewComment{
			{Path: "foo.go", Line: 11},                              // default RIGHT
			{Path: "foo.go", Line: 12, Side: "RIGHT"},               // explicit RIGHT
			{Path: "foo.go", Line: 10, Side: "LEFT"},                // LEFT side
			{Path: "foo.go", Line: 11, StartLine: &start},           // multi-line
		},
	}
	if err := validateCommentsAgainstDiff(sub, diffs); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateCommentsAgainstDiff_PathNotInPR(t *testing.T) {
	diffs := map[string]*prFileDiff{
		"foo.go": {Path: "foo.go", Patch: "x", rightLines: map[int]bool{1: true}},
	}
	sub := reviewSubmission{
		Comments: []reviewComment{{Path: "bar.go", Line: 1}},
	}
	err := validateCommentsAgainstDiff(sub, diffs)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bar.go") || !strings.Contains(err.Error(), "not in the PR") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCommentsAgainstDiff_RenamedHint(t *testing.T) {
	diffs := map[string]*prFileDiff{
		"new.go": {
			Path:             "new.go",
			PreviousFilename: "old.go",
			Patch:            "x",
			rightLines:       map[int]bool{1: true},
		},
	}
	sub := reviewSubmission{
		Comments: []reviewComment{{Path: "old.go", Line: 1}},
	}
	err := validateCommentsAgainstDiff(sub, diffs)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "renamed to \"new.go\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCommentsAgainstDiff_LineNotInDiff(t *testing.T) {
	diffs := map[string]*prFileDiff{
		"foo.go": {
			Path:       "foo.go",
			Patch:      "non-empty",
			rightLines: map[int]bool{10: true},
		},
	}
	sub := reviewSubmission{
		Comments: []reviewComment{{Path: "foo.go", Line: 42}},
	}
	err := validateCommentsAgainstDiff(sub, diffs)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "foo.go:42") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "not in the diff") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCommentsAgainstDiff_NoLeftSideForAddedFile(t *testing.T) {
	diffs := map[string]*prFileDiff{
		"new.go": {
			Path:       "new.go",
			Patch:      "x",
			leftLines:  map[int]bool{},
			rightLines: map[int]bool{1: true, 2: true},
		},
	}
	sub := reviewSubmission{
		Comments: []reviewComment{{Path: "new.go", Line: 1, Side: "LEFT"}},
	}
	err := validateCommentsAgainstDiff(sub, diffs)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no LEFT-side lines exist") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCommentsAgainstDiff_RangeStartInvalid(t *testing.T) {
	diffs := map[string]*prFileDiff{
		"foo.go": {
			Path:       "foo.go",
			Patch:      "x",
			rightLines: map[int]bool{10: true, 11: true},
		},
	}
	start := 5
	sub := reviewSubmission{
		Comments: []reviewComment{{Path: "foo.go", Line: 11, StartLine: &start}},
	}
	err := validateCommentsAgainstDiff(sub, diffs)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "range start") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCommentsAgainstDiff_FileLevelOK(t *testing.T) {
	diffs := map[string]*prFileDiff{
		"foo.go": {Path: "foo.go"},
	}
	sub := reviewSubmission{
		Comments: []reviewComment{{Path: "foo.go", SubjectFile: true}},
	}
	if err := validateCommentsAgainstDiff(sub, diffs); err != nil {
		t.Errorf("file-level comment on existing file should pass: %v", err)
	}
}

func TestValidateCommentsAgainstDiff_FileLevelMissingPath(t *testing.T) {
	diffs := map[string]*prFileDiff{}
	sub := reviewSubmission{
		Comments: []reviewComment{{Path: "ghost.go", SubjectFile: true}},
	}
	if err := validateCommentsAgainstDiff(sub, diffs); err == nil {
		t.Fatal("expected error for file-level comment on missing file")
	}
}

func TestValidateCommentsAgainstDiff_EmptyPatchSkipsLineCheck(t *testing.T) {
	// Binary / too-large file — patch is empty. Line validation is skipped.
	diffs := map[string]*prFileDiff{
		"image.png": {Path: "image.png", Patch: ""},
	}
	sub := reviewSubmission{
		Comments: []reviewComment{{Path: "image.png", Line: 999}},
	}
	if err := validateCommentsAgainstDiff(sub, diffs); err != nil {
		t.Errorf("expected no error for empty-patch file, got: %v", err)
	}
}

func TestValidateCommentsAgainstDiff_AggregatesMultipleErrors(t *testing.T) {
	diffs := map[string]*prFileDiff{
		"foo.go": {
			Path:       "foo.go",
			Patch:      "x",
			rightLines: map[int]bool{10: true},
		},
		"renamed_new.go": {
			Path:             "renamed_new.go",
			PreviousFilename: "renamed_old.go",
			Patch:            "x",
			rightLines:       map[int]bool{1: true},
		},
	}
	sub := reviewSubmission{
		Comments: []reviewComment{
			{Path: "foo.go", Line: 42},        // line not in diff
			{Path: "ghost.go", Line: 1},       // path not in PR
			{Path: "renamed_old.go", Line: 1}, // renamed
			{Path: "foo.go", Line: 10},        // valid — should NOT appear in error
		},
	}
	err := validateCommentsAgainstDiff(sub, diffs)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "3 invalid review comment(s):") {
		t.Errorf("expected aggregate header, got: %s", msg)
	}
	for _, want := range []string{"foo.go:42", "ghost.go", "renamed_old.go", "renamed_new.go"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected message to contain %q, got:\n%s", want, msg)
		}
	}
	// Valid comment should not appear in the error text.
	if strings.Count(msg, "foo.go") > 1 || strings.Contains(msg, "foo.go:10") {
		// "foo.go" appears once in the invalid line entry; "foo.go:10" (the valid one) must not be mentioned.
		if strings.Contains(msg, "foo.go:10") {
			t.Errorf("valid comment leaked into error: %s", msg)
		}
	}
}

func TestValidateCommentsAgainstDiff_SingleErrorNoAggregateHeader(t *testing.T) {
	diffs := map[string]*prFileDiff{
		"foo.go": {Path: "foo.go", Patch: "x", rightLines: map[int]bool{10: true}},
	}
	sub := reviewSubmission{
		Comments: []reviewComment{{Path: "foo.go", Line: 42}},
	}
	err := validateCommentsAgainstDiff(sub, diffs)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "invalid review comment(s):") {
		t.Errorf("single error should not include aggregate header: %s", err.Error())
	}
}

func TestDecodePaginatedFiles_SingleArray(t *testing.T) {
	in := `[{"filename":"a.go","status":"modified","patch":"x"}]`
	files, err := decodePaginatedFiles(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Filename != "a.go" {
		t.Errorf("unexpected: %+v", files)
	}
}

func TestDecodePaginatedFiles_ConcatenatedArrays(t *testing.T) {
	// Older gh versions emit concatenated arrays without merging.
	in := `[{"filename":"a.go"}][{"filename":"b.go"}]`
	files, err := decodePaginatedFiles(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Filename != "a.go" || files[1].Filename != "b.go" {
		t.Errorf("unexpected: %+v", files)
	}
}
