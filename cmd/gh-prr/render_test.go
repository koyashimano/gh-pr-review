package main

import (
	"strings"
	"testing"
)

func TestRenderMarkdown_ReviewSummaries(t *testing.T) {
	pr := pullRequest{Number: 7, Title: "T", URL: "https://example.com/pr/7"}
	reviews := []prReviewNode{
		{
			Author:      &user{Login: "alice"},
			State:       "CHANGES_REQUESTED",
			Body:        "Overall this needs work.",
			SubmittedAt: "2026-05-30T10:00:00Z",
			URL:         "https://example.com/review/2",
		},
		{
			Author:      &user{Login: "bob"},
			State:       "APPROVED",
			Body:        "",
			SubmittedAt: "2026-05-29T10:00:00Z",
			URL:         "https://example.com/review/1",
		},
	}
	out := renderMarkdown(pr, nil, reviews, 3, false)

	if !strings.Contains(out, "## Review by alice — CHANGES_REQUESTED") {
		t.Errorf("missing alice summary header:\n%s", out)
	}
	if !strings.Contains(out, "Overall this needs work.") {
		t.Errorf("missing alice summary body:\n%s", out)
	}
	if !strings.Contains(out, "## Review by bob — APPROVED") {
		t.Errorf("missing bob approval header:\n%s", out)
	}
	if !strings.Contains(out, "_(no summary)_") {
		t.Errorf("approval with empty body should render placeholder:\n%s", out)
	}

	// Sorted oldest-first: bob (29th) before alice (30th).
	if strings.Index(out, "Review by bob") > strings.Index(out, "Review by alice") {
		t.Errorf("summaries not sorted by submittedAt ascending:\n%s", out)
	}
}

func TestRenderMarkdown_SkipsPendingAndEmptyCommented(t *testing.T) {
	pr := pullRequest{Number: 1, Title: "T", URL: "u"}
	reviews := []prReviewNode{
		{Author: &user{Login: "me"}, State: "PENDING", Body: "draft summary"},
		{Author: &user{Login: "inline"}, State: "COMMENTED", Body: ""},
	}
	out := renderMarkdown(pr, nil, reviews, 3, false)

	if strings.Contains(out, "Review by") {
		t.Errorf("pending and empty-bodied COMMENTED reviews should be skipped:\n%s", out)
	}
	if strings.Contains(out, "draft summary") {
		t.Errorf("pending review body must not leak into export:\n%s", out)
	}
}

func TestRenderMarkdown_NoReviews(t *testing.T) {
	pr := pullRequest{Number: 1, Title: "T", URL: "u"}
	out := renderMarkdown(pr, nil, nil, 3, false)
	if strings.Contains(out, "Review by") {
		t.Errorf("no review section expected when there are no reviews:\n%s", out)
	}
}
