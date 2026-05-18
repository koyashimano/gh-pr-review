package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

type submitReviewCommentJSON struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Body      string `json:"body"`
	Side      string `json:"side,omitempty"`
	StartLine *int   `json:"start_line,omitempty"`
	StartSide string `json:"start_side,omitempty"`
}

type submitReviewRequestJSON struct {
	CommitID string                    `json:"commit_id,omitempty"`
	Body     string                    `json:"body"`
	Event    string                    `json:"event,omitempty"`
	Comments []submitReviewCommentJSON `json:"comments,omitempty"`
}

func submitReview(owner, repo string, prNumber int, sub reviewSubmission, finalize bool) (string, string, error) {
	if _, existing, err := fetchPendingReview(owner, repo, prNumber); err != nil {
		return "", "", fmt.Errorf("failed to check for an existing pending review: %w", err)
	} else if existing != nil {
		return "", "", fmt.Errorf("you already have a pending review on this PR (%d inline comment(s) so far); inspect with `gh-prr pending`, finalize with `gh-prr submit-pending`, or delete it via the GitHub UI before creating a new one", len(existing.Comments.Nodes))
	}

	var lineComments, fileComments []reviewComment
	for _, c := range sub.Comments {
		if c.SubjectFile {
			fileComments = append(fileComments, c)
		} else {
			lineComments = append(lineComments, c)
		}
	}

	hasFileComments := len(fileComments) > 0
	// File-level comments must be added via GraphQL against a pending review.
	// When the user wants the review finalized, we still create it as pending
	// first, attach the file-level threads, then submit.
	initialPending := !finalize || hasFileComments

	req := submitReviewRequestJSON{
		CommitID: sub.CommitID,
		Body:     sub.Body,
	}
	if !initialPending {
		req.Event = sub.Event
	}
	for _, c := range lineComments {
		req.Comments = append(req.Comments, submitReviewCommentJSON{
			Path:      c.Path,
			Line:      c.Line,
			Body:      c.Body,
			Side:      c.Side,
			StartLine: c.StartLine,
			StartSide: c.StartSide,
		})
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to encode review request: %w", err)
	}

	cmd := []string{
		"gh", "api",
		"-X", "POST",
		"-H", "Accept: application/vnd.github+json",
		fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", owner, repo, prNumber),
		"--input", "-",
	}

	out, err := runWithStdin(cmd, body)
	if err != nil {
		return "", "", err
	}

	var resp struct {
		NodeID  string `json:"node_id"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", "", fmt.Errorf("failed to parse review creation response: %w", err)
	}

	if hasFileComments {
		if resp.NodeID == "" {
			return resp.HTMLURL, resp.State, fmt.Errorf("review created but no node_id returned; cannot attach file-level comments")
		}
		for i, c := range fileComments {
			if err := addFileLevelThread(resp.NodeID, c.Path, c.Body); err != nil {
				fmt.Fprintf(os.Stderr, "warning: review left pending with %d/%d file-level comment(s) attached\n", i, len(fileComments))
				fmt.Fprintf(os.Stderr, "hint: finish or delete the pending review via the GitHub UI, or finalize with `gh-prr submit-pending -e %s`\n", sub.Event)
				return resp.HTMLURL, resp.State, fmt.Errorf("failed to attach file-level comment for %s: %w", c.Path, err)
			}
		}
	}

	if hasFileComments && finalize {
		url, state, err := submitPendingReview(resp.NodeID, sub.Event)
		if err != nil {
			fmt.Fprintln(os.Stderr, "warning: review and all comments created, but finalize step failed; review left pending")
			fmt.Fprintf(os.Stderr, "hint: finalize with `gh-prr submit-pending -e %s`\n", sub.Event)
			return resp.HTMLURL, resp.State, fmt.Errorf("failed to finalize review: %w", err)
		}
		return url, state, nil
	}

	return resp.HTMLURL, resp.State, nil
}

func addFileLevelThread(reviewID, path, body string) error {
	cmd := []string{
		"gh", "api", "graphql",
		"-f", fmt.Sprintf("id=%s", reviewID),
		"-f", fmt.Sprintf("path=%s", path),
		"-f", fmt.Sprintf("body=%s", body),
		"-f", fmt.Sprintf("query=%s", addFileLevelThreadMutation),
	}

	var resp struct {
		Data struct {
			AddPullRequestReviewThread struct {
				Thread struct {
					ID string `json:"id"`
				} `json:"thread"`
			} `json:"addPullRequestReviewThread"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}

	if err := ghJSON(cmd, &resp); err != nil {
		return err
	}

	if len(resp.Errors) > 0 {
		blob, _ := json.Marshal(resp.Errors)
		return fmt.Errorf("GraphQL errors: %s", string(blob))
	}

	if resp.Data.AddPullRequestReviewThread.Thread.ID == "" {
		return errors.New("addPullRequestReviewThread returned no thread id")
	}

	return nil
}

func submitPendingReview(reviewID, event string) (string, string, error) {
	cmd := []string{
		"gh", "api", "graphql",
		"-F", fmt.Sprintf("id=%s", reviewID),
		"-F", fmt.Sprintf("event=%s", event),
		"-f", fmt.Sprintf("query=%s", submitPullRequestReviewMutation),
	}

	var resp struct {
		Data struct {
			SubmitPullRequestReview struct {
				PullRequestReview struct {
					State string `json:"state"`
					URL   string `json:"url"`
				} `json:"pullRequestReview"`
			} `json:"submitPullRequestReview"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}

	if err := ghJSON(cmd, &resp); err != nil {
		return "", "", err
	}

	if len(resp.Errors) > 0 {
		blob, _ := json.Marshal(resp.Errors)
		return "", "", fmt.Errorf("GraphQL errors: %s", string(blob))
	}

	review := resp.Data.SubmitPullRequestReview.PullRequestReview
	return review.URL, review.State, nil
}

func readReviewFile(path string) (string, error) {
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("failed to read review from stdin: %w", err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read review file %q: %w", path, err)
	}
	return string(data), nil
}

func runSubmit(owner, repo string, opts submitOptions) error {
	content, err := readReviewFile(opts.file)
	if err != nil {
		return err
	}

	sub, err := parseReviewMarkdown(content)
	if err != nil {
		return err
	}

	if err := validateReviewSubmission(sub, opts.finalize); err != nil {
		return err
	}

	if !opts.finalize && (sub.Event == "APPROVE" || sub.Event == "REQUEST_CHANGES") {
		fmt.Fprintf(os.Stderr, "warning: review will be saved as pending; front matter event %q will be ignored (use `gh-prr submit-pending -e %s` after to finalize, or pass --finalize to submit immediately)\n", sub.Event, sub.Event)
	}

	prNumber, err := resolvePRNumber(opts.prNumber)
	if err != nil {
		return err
	}

	url, state, err := submitReview(owner, repo, prNumber, sub, opts.finalize)
	if err != nil {
		return err
	}

	if opts.finalize {
		fmt.Printf("Submitted review (%d inline comment(s)). State: %s\n", len(sub.Comments), state)
	} else {
		fmt.Printf("Created pending review (%d inline comment(s)). State: %s\n", len(sub.Comments), state)
	}
	if url != "" {
		fmt.Println(url)
	}
	return nil
}

func runSubmitPending(owner, repo string, opts submitPendingOptions) error {
	prNumber, err := resolvePRNumber(opts.prNumber)
	if err != nil {
		return err
	}

	_, review, err := fetchPendingReview(owner, repo, prNumber)
	if err != nil {
		return err
	}
	if review == nil {
		return errors.New("no pending review found")
	}

	url, state, err := submitPendingReview(review.ID, opts.event)
	if err != nil {
		return err
	}

	fmt.Printf("Submitted pending review. State: %s\n", state)
	if url != "" {
		fmt.Println(url)
	}
	return nil
}
