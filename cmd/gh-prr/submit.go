package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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

func submitReview(owner, repo string, prNumber int, sub reviewSubmission, finalize, yes bool) (string, string, bool, error) {
	_, existing, err := fetchPendingReview(owner, repo, prNumber)
	if err != nil {
		return "", "", false, fmt.Errorf("failed to check for an existing pending review: %w", err)
	}
	if existing != nil {
		if !yes {
			ok, err := confirmAppendToPending(existing, finalize)
			if err != nil {
				return "", "", false, err
			}
			if !ok {
				return "", "", false, errors.New("aborted: existing pending review left untouched (inspect with `gh-prr pending`, finalize with `gh-prr submit-pending`, or delete it via the GitHub UI)")
			}
		}
		url, state, err := appendToPendingReview(existing, sub, finalize)
		return url, state, true, err
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
		return "", "", false, fmt.Errorf("failed to encode review request: %w", err)
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
		return "", "", false, err
	}

	var resp struct {
		NodeID  string `json:"node_id"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", "", false, fmt.Errorf("failed to parse review creation response: %w", err)
	}

	if hasFileComments {
		if resp.NodeID == "" {
			return resp.HTMLURL, resp.State, false, fmt.Errorf("review created but no node_id returned; cannot attach file-level comments")
		}
		for i, c := range fileComments {
			if err := addFileLevelThread(resp.NodeID, c.Path, c.Body); err != nil {
				fmt.Fprintf(os.Stderr, "warning: review left pending with %d/%d file-level comment(s) attached\n", i, len(fileComments))
				fmt.Fprintf(os.Stderr, "hint: finish or delete the pending review via the GitHub UI, or finalize with `gh-prr submit-pending -e %s`\n", sub.Event)
				return resp.HTMLURL, resp.State, false, fmt.Errorf("failed to attach file-level comment for %s: %w", c.Path, err)
			}
		}
	}

	if hasFileComments && finalize {
		url, state, err := submitPendingReview(resp.NodeID, sub.Event)
		if err != nil {
			fmt.Fprintln(os.Stderr, "warning: review and all comments created, but finalize step failed; review left pending")
			fmt.Fprintf(os.Stderr, "hint: finalize with `gh-prr submit-pending -e %s`\n", sub.Event)
			return resp.HTMLURL, resp.State, false, fmt.Errorf("failed to finalize review: %w", err)
		}
		return url, state, false, nil
	}

	return resp.HTMLURL, resp.State, false, nil
}

func confirmAppendToPending(existing *pendingReview, finalize bool) (bool, error) {
	action := "Append the new comments to it"
	if finalize {
		action = "Append the new comments and finalize the review"
	}
	msg := fmt.Sprintf("You already have a pending review on this PR (%d comment(s) so far). %s? [y/N]: ", len(existing.Comments.Nodes), action)
	return promptYesNo(msg)
}

func promptYesNo(msg string) (bool, error) {
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		defer tty.Close()
		if _, err := fmt.Fprint(tty, msg); err != nil {
			return false, fmt.Errorf("failed to write prompt: %w", err)
		}
		line, err := bufio.NewReader(tty).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return false, fmt.Errorf("failed to read confirmation: %w", err)
		}
		return parseYesNo(line), nil
	}

	fi, statErr := os.Stdin.Stat()
	if statErr != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		return false, errors.New("cannot prompt for confirmation: stdin is not a terminal and /dev/tty is unavailable; re-run with -y/--yes to confirm non-interactively")
	}
	fmt.Fprint(os.Stderr, msg)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("failed to read confirmation: %w", err)
	}
	return parseYesNo(line), nil
}

func parseYesNo(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}

func appendToPendingReview(existing *pendingReview, sub reviewSubmission, finalize bool) (string, string, error) {
	if strings.TrimSpace(sub.Body) != "" {
		newBody := sub.Body
		if strings.TrimSpace(existing.Body) != "" {
			newBody = existing.Body + "\n\n" + sub.Body
		}
		if err := updatePullRequestReviewBody(existing.ID, newBody); err != nil {
			return existing.URL, "PENDING", fmt.Errorf("failed to update pending review body: %w", err)
		}
	}

	for i, c := range sub.Comments {
		var err error
		if c.SubjectFile {
			err = addFileLevelThread(existing.ID, c.Path, c.Body)
		} else {
			err = addInlineThread(existing.ID, c)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %d/%d new comment(s) attached before failure\n", i, len(sub.Comments))
			fmt.Fprintln(os.Stderr, "hint: inspect with `gh-prr pending`; the pending review has been partially updated")
			return existing.URL, "PENDING", fmt.Errorf("failed to attach comment for %s: %w", c.Path, err)
		}
	}

	if finalize {
		url, state, err := submitPendingReview(existing.ID, sub.Event)
		if err != nil {
			fmt.Fprintln(os.Stderr, "warning: comments appended, but finalize step failed; review left pending")
			fmt.Fprintf(os.Stderr, "hint: finalize with `gh-prr submit-pending -e %s`\n", sub.Event)
			return existing.URL, "PENDING", fmt.Errorf("failed to finalize review: %w", err)
		}
		return url, state, nil
	}

	return existing.URL, "PENDING", nil
}

func updatePullRequestReviewBody(reviewID, body string) error {
	cmd := []string{
		"gh", "api", "graphql",
		"-f", fmt.Sprintf("id=%s", reviewID),
		"-f", fmt.Sprintf("body=%s", body),
		"-f", fmt.Sprintf("query=%s", updatePullRequestReviewBodyMutation),
	}

	var resp struct {
		Data struct {
			UpdatePullRequestReview struct {
				PullRequestReview struct {
					URL string `json:"url"`
				} `json:"pullRequestReview"`
			} `json:"updatePullRequestReview"`
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

	return nil
}

func addInlineThread(reviewID string, c reviewComment) error {
	side := c.Side
	if side == "" {
		side = "RIGHT"
	}

	cmd := []string{
		"gh", "api", "graphql",
		"-f", fmt.Sprintf("id=%s", reviewID),
		"-f", fmt.Sprintf("path=%s", c.Path),
		"-f", fmt.Sprintf("body=%s", c.Body),
		"-F", fmt.Sprintf("line=%d", c.Line),
		"-f", fmt.Sprintf("side=%s", side),
	}

	query := addInlineThreadMutation
	if c.StartLine != nil {
		startSide := c.StartSide
		if startSide == "" {
			startSide = side
		}
		cmd = append(cmd,
			"-F", fmt.Sprintf("startLine=%d", *c.StartLine),
			"-f", fmt.Sprintf("startSide=%s", startSide),
		)
		query = addInlineThreadRangeMutation
	}

	cmd = append(cmd, "-f", fmt.Sprintf("query=%s", query))

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

	if len(sub.Comments) > 0 {
		diffs, err := fetchPRDiffs(owner, repo, prNumber)
		if err != nil {
			return err
		}
		if err := validateCommentsAgainstDiff(sub, diffs); err != nil {
			return err
		}
	}

	url, state, appended, err := submitReview(owner, repo, prNumber, sub, opts.finalize, opts.yes)
	if err != nil {
		return err
	}

	switch {
	case appended && opts.finalize:
		fmt.Printf("Appended to pending review and finalized (%d new comment(s)). State: %s\n", len(sub.Comments), state)
	case appended:
		fmt.Printf("Appended to pending review (%d new comment(s)). State: %s\n", len(sub.Comments), state)
	case opts.finalize:
		fmt.Printf("Submitted review (%d comment(s)). State: %s\n", len(sub.Comments), state)
	default:
		fmt.Printf("Created pending review (%d comment(s)). State: %s\n", len(sub.Comments), state)
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
