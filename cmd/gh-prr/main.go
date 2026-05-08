package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type command string

const (
	cmdExport        command = "export"
	cmdResolve       command = "resolve"
	cmdPending       command = "pending"
	cmdWait          command = "wait"
	cmdSubmit        command = "submit"
	cmdSubmitPending command = "submit-pending"
)

var errTimeout = errors.New("timeout: no new review detected")

const gqlQuery = `
query($owner: String!, $name: String!, $number: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      number
      title
      url
      reviewThreads(first: 100, after: $after) {
        totalCount
        nodes {
          id
          isResolved
          isOutdated
          isCollapsed
          path
          line
          startLine
          originalLine
          originalStartLine
          diffSide
          startDiffSide
          resolvedBy { login }
          comments(first: 100) {
            totalCount
            nodes {
              id
              url
              createdAt
              body
              diffHunk
              author { login }
              path
              line
              startLine
              originalLine
              originalStartLine
              position
              originalPosition
            }
            pageInfo { hasNextPage endCursor }
          }
        }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}
`

const commentPageQuery = `
query($id: ID!, $after: String) {
  node(id: $id) {
    ... on PullRequestReviewThread {
      comments(first: 100, after: $after) {
        totalCount
        nodes {
          id
          url
          createdAt
          body
          diffHunk
          author { login }
          path
          line
          startLine
          originalLine
          originalStartLine
          position
          originalPosition
        }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}
`

const unresolvedThreadsQuery = `
query($owner: String!, $name: String!, $number: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100, after: $after) {
        totalCount
        nodes {
          id
          isResolved
        }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}
`

const resolveThreadMutation = `
mutation($id:ID!) {
  resolveReviewThread(input:{threadId:$id}) {
    thread {
      isResolved
    }
  }
}
`

const submitPullRequestReviewMutation = `
mutation($id: ID!, $event: PullRequestReviewEvent!) {
  submitPullRequestReview(input: { pullRequestReviewId: $id, event: $event }) {
    pullRequestReview {
      state
      url
    }
  }
}
`

const addFileLevelThreadMutation = `
mutation($id: ID!, $path: String!, $body: String!) {
  addPullRequestReviewThread(input: {
    pullRequestReviewId: $id,
    path: $path,
    body: $body,
    subjectType: FILE
  }) {
    thread { id }
  }
}
`

const pendingReviewQuery = `
query($owner: String!, $name: String!, $number: Int!) {
  viewer { login }
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      number
      title
      url
      reviews(first: 10, states: [PENDING]) {
        nodes {
          id
          author { login }
          body
          comments(first: 100) {
            totalCount
            nodes {
              id
              url
              body
              path
              position
              originalPosition
              diffHunk
              createdAt
              line
              startLine
              originalLine
              originalStartLine
            }
            pageInfo { hasNextPage endCursor }
          }
        }
      }
    }
  }
}
`

const pendingReviewCommentPageQuery = `
query($id: ID!, $after: String) {
  node(id: $id) {
    ... on PullRequestReview {
      comments(first: 100, after: $after) {
        totalCount
        nodes {
          id
          url
          body
          path
          position
          originalPosition
          diffHunk
          createdAt
          line
          startLine
          originalLine
          originalStartLine
        }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}
`

type graphQLResponse struct {
	Data struct {
		Repository struct {
			PullRequest pullRequest `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type graphQLError struct {
	Message string `json:"message"`
}

type pullRequest struct {
	Number        int                    `json:"number"`
	Title         string                 `json:"title"`
	URL           string                 `json:"url"`
	ReviewThreads reviewThreadConnection `json:"reviewThreads"`
}

type reviewThreadConnection struct {
	TotalCount int            `json:"totalCount"`
	Nodes      []reviewThread `json:"nodes"`
	PageInfo   pageInfo       `json:"pageInfo"`
}

type reviewThread struct {
	ID                string            `json:"id"`
	IsResolved        bool              `json:"isResolved"`
	IsOutdated        bool              `json:"isOutdated"`
	IsCollapsed       bool              `json:"isCollapsed"`
	Path              string            `json:"path"`
	Line              *int              `json:"line"`
	StartLine         *int              `json:"startLine"`
	OriginalLine      *int              `json:"originalLine"`
	OriginalStartLine *int              `json:"originalStartLine"`
	DiffSide          string            `json:"diffSide"`
	StartDiffSide     string            `json:"startDiffSide"`
	ResolvedBy        *user             `json:"resolvedBy"`
	Comments          commentConnection `json:"comments"`
}

type commentConnection struct {
	TotalCount int       `json:"totalCount"`
	Nodes      []comment `json:"nodes"`
	PageInfo   pageInfo  `json:"pageInfo"`
}

type comment struct {
	ID                string `json:"id"`
	URL               string `json:"url"`
	CreatedAt         string `json:"createdAt"`
	Body              string `json:"body"`
	DiffHunk          string `json:"diffHunk"`
	Author            *user  `json:"author"`
	Path              string `json:"path"`
	Line              *int   `json:"line"`
	StartLine         *int   `json:"startLine"`
	OriginalLine      *int   `json:"originalLine"`
	OriginalStartLine *int   `json:"originalStartLine"`
	Position          *int   `json:"position"`
	OriginalPosition  *int   `json:"originalPosition"`
}

type user struct {
	Login string `json:"login"`
}

type prReview struct {
	User  *user  `json:"user"`
	State string `json:"state"`
	Body  string `json:"body"`
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type pendingReviewResponse struct {
	Data struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
		Repository struct {
			PullRequest struct {
				Number  int    `json:"number"`
				Title   string `json:"title"`
				URL     string `json:"url"`
				Reviews struct {
					Nodes []pendingReview `json:"nodes"`
				} `json:"reviews"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type pendingReview struct {
	ID       string            `json:"id"`
	Author   *user             `json:"author"`
	Body     string            `json:"body"`
	Comments commentConnection `json:"comments"`
}

type exportOptions struct {
	ctx            int
	unresolvedOnly bool
	prNumber       *int
}

type resolveOptions struct {
	prNumber *int
}

type pendingOptions struct {
	ctx      int
	prNumber *int
}

type waitOptions struct {
	interval int
	timeout  int
	prNumber *int
}

type submitOptions struct {
	file     string
	pending  bool
	prNumber *int
}

type submitPendingOptions struct {
	event    string
	prNumber *int
}

type parsedArgs struct {
	cmd           command
	export        exportOptions
	resolve       resolveOptions
	pending       pendingOptions
	wait          waitOptions
	submit        submitOptions
	submitPending submitPendingOptions
}

type reviewComment struct {
	Path        string
	Line        int
	StartLine   *int
	Side        string
	StartSide   string
	Body        string
	SubjectFile bool
}

type reviewSubmission struct {
	Event    string
	CommitID string
	Body     string
	Comments []reviewComment
}

func run(cmd []string) (string, error) {
	// run executes the given command and returns its stdout; stderr is captured for error reporting.
	if len(cmd) == 0 {
		return "", errors.New("empty command")
	}

	execCmd := exec.Command(cmd[0], cmd[1:]...)

	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	if err := execCmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("command failed: %s", strings.Join(cmd, " "))
		}
		return "", errors.New(msg)
	}

	return stdout.String(), nil
}

func runWithStdin(cmd []string, stdin []byte) (string, error) {
	if len(cmd) == 0 {
		return "", errors.New("empty command")
	}

	execCmd := exec.Command(cmd[0], cmd[1:]...)
	execCmd.Stdin = bytes.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	if err := execCmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("command failed: %s", strings.Join(cmd, " "))
		}
		return "", errors.New(msg)
	}

	return stdout.String(), nil
}

func ghJSON(cmd []string, v any) error {
	// ghJSON runs a command expected to return JSON and decodes it into v using json.Decoder.
	out, err := run(cmd)
	if err != nil {
		return err
	}

	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("command returned empty output: expected JSON response from %q", strings.Join(cmd, " "))
	}

	dec := json.NewDecoder(strings.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	return nil
}

func getOwnerRepo() (string, string, error) {
	var resp struct {
		NameWithOwner string `json:"nameWithOwner"`
	}

	if err := ghJSON([]string{"gh", "repo", "view", "--json", "nameWithOwner"}, &resp); err != nil {
		return "", "", err
	}

	parts := strings.SplitN(resp.NameWithOwner, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected nameWithOwner: %q", resp.NameWithOwner)
	}

	return parts[0], parts[1], nil
}

func resolvePRNumber(provided *int) (int, error) {
	if provided != nil {
		return *provided, nil
	}

	var resp struct {
		Number json.Number `json:"number"`
	}

	if err := ghJSON([]string{"gh", "pr", "view", "--json", "number"}, &resp); err != nil {
		return 0, fmt.Errorf("failed to resolve PR number from current branch: %w", err)
	}

	if resp.Number == "" {
		return 0, fmt.Errorf("PR number not found for current branch. Specify a PR number explicitly or ensure you are on a branch with an open pull request")
	}

	prNumber, parseErr := strconv.Atoi(resp.Number.String())
	if parseErr != nil {
		return 0, fmt.Errorf("failed to parse PR number %q from gh JSON output: %w", resp.Number.String(), parseErr)
	}

	return prNumber, nil
}

func fetchThreads(owner, repo string, prNumber int) (pullRequest, []reviewThread, error) {
	var prInfo pullRequest
	var threads []reviewThread
	var after *string

	for {
		cmd := []string{
			"gh", "api", "graphql",
			"-F", fmt.Sprintf("owner=%s", owner),
			"-F", fmt.Sprintf("name=%s", repo),
			"-F", fmt.Sprintf("number=%d", prNumber),
		}

		if after != nil {
			cmd = append(cmd, "-F", fmt.Sprintf("after=%s", *after))
		}

		cmd = append(cmd, "-f", fmt.Sprintf("query=%s", gqlQuery))

		var resp graphQLResponse
		if err := ghJSON(cmd, &resp); err != nil {
			return prInfo, nil, err
		}

		if len(resp.Errors) > 0 {
			blob, _ := json.Marshal(resp.Errors)
			return prInfo, nil, fmt.Errorf("GraphQL errors: %s", string(blob))
		}

		pr := resp.Data.Repository.PullRequest
		if pr.Number == 0 && pr.Title == "" && pr.URL == "" {
			return prInfo, nil, fmt.Errorf("pull request #%d in %s/%s not found or GraphQL query returned no data; verify the PR number and that you have access to the repository", prNumber, owner, repo)
		}

		if prInfo.Number == 0 {
			prInfo = pullRequest{
				Number: pr.Number,
				Title:  pr.Title,
				URL:    pr.URL,
			}
		}

		if threads == nil {
			estimated := pr.ReviewThreads.TotalCount
			if estimated <= 0 {
				estimated = len(pr.ReviewThreads.Nodes)
			}
			if estimated > 0 {
				threads = make([]reviewThread, 0, estimated)
			}
		}

		for _, thread := range pr.ReviewThreads.Nodes {
			if thread.Comments.PageInfo.HasNextPage {
				fullComments, err := fetchAllComments(thread.ID, thread.Comments)
				if err != nil {
					return prInfo, nil, err
				}
				thread.Comments = fullComments
			}
			threads = append(threads, thread)
		}

		if pr.ReviewThreads.PageInfo.HasNextPage && pr.ReviewThreads.PageInfo.EndCursor != "" {
			cursor := pr.ReviewThreads.PageInfo.EndCursor
			after = &cursor
			continue
		}

		break
	}

	return prInfo, threads, nil
}

func fetchAllComments(threadID string, existing commentConnection) (commentConnection, error) {
	out := existing
	after := existing.PageInfo.EndCursor

	for existing.PageInfo.HasNextPage && after != "" {
		cmd := []string{
			"gh", "api", "graphql",
			"-F", fmt.Sprintf("id=%s", threadID),
			"-F", fmt.Sprintf("after=%s", after),
			"-f", fmt.Sprintf("query=%s", commentPageQuery),
		}

		var resp struct {
			Data struct {
				Node struct {
					Comments commentConnection `json:"comments"`
				} `json:"node"`
			} `json:"data"`
			Errors []graphQLError `json:"errors"`
		}

		if err := ghJSON(cmd, &resp); err != nil {
			return out, err
		}

		if len(resp.Errors) > 0 {
			blob, _ := json.Marshal(resp.Errors)
			return out, fmt.Errorf("GraphQL errors: %s", string(blob))
		}

		out.Nodes = append(out.Nodes, resp.Data.Node.Comments.Nodes...)
		out.TotalCount = resp.Data.Node.Comments.TotalCount
		out.PageInfo = resp.Data.Node.Comments.PageInfo
		after = out.PageInfo.EndCursor
		existing.PageInfo = out.PageInfo
	}

	return out, nil
}

func fetchPendingReview(owner, repo string, prNumber int) (pullRequest, *pendingReview, error) {
	cmd := []string{
		"gh", "api", "graphql",
		"-F", fmt.Sprintf("owner=%s", owner),
		"-F", fmt.Sprintf("name=%s", repo),
		"-F", fmt.Sprintf("number=%d", prNumber),
		"-f", fmt.Sprintf("query=%s", pendingReviewQuery),
	}

	var resp pendingReviewResponse
	if err := ghJSON(cmd, &resp); err != nil {
		return pullRequest{}, nil, err
	}

	if len(resp.Errors) > 0 {
		blob, _ := json.Marshal(resp.Errors)
		return pullRequest{}, nil, fmt.Errorf("GraphQL errors: %s", string(blob))
	}

	pr := resp.Data.Repository.PullRequest
	if pr.Number == 0 && pr.Title == "" && pr.URL == "" {
		return pullRequest{}, nil, fmt.Errorf("pull request #%d in %s/%s not found or GraphQL query returned no data; verify the PR number and that you have access to the repository", prNumber, owner, repo)
	}

	prInfo := pullRequest{
		Number: pr.Number,
		Title:  pr.Title,
		URL:    pr.URL,
	}

	viewerLogin := resp.Data.Viewer.Login
	var review *pendingReview
	for i := range pr.Reviews.Nodes {
		r := pr.Reviews.Nodes[i]
		if r.Author != nil && r.Author.Login == viewerLogin {
			review = &r
			break
		}
	}
	if review == nil {
		return prInfo, nil, nil
	}

	if review.Comments.PageInfo.HasNextPage {
		fullComments, err := fetchAllPendingReviewComments(review.ID, review.Comments)
		if err != nil {
			return prInfo, nil, err
		}
		review.Comments = fullComments
	}

	return prInfo, review, nil
}

func fetchAllPendingReviewComments(reviewID string, existing commentConnection) (commentConnection, error) {
	out := existing
	after := existing.PageInfo.EndCursor

	for existing.PageInfo.HasNextPage && after != "" {
		cmd := []string{
			"gh", "api", "graphql",
			"-F", fmt.Sprintf("id=%s", reviewID),
			"-F", fmt.Sprintf("after=%s", after),
			"-f", fmt.Sprintf("query=%s", pendingReviewCommentPageQuery),
		}

		var resp struct {
			Data struct {
				Node struct {
					Comments commentConnection `json:"comments"`
				} `json:"node"`
			} `json:"data"`
			Errors []graphQLError `json:"errors"`
		}

		if err := ghJSON(cmd, &resp); err != nil {
			return out, err
		}

		if len(resp.Errors) > 0 {
			blob, _ := json.Marshal(resp.Errors)
			return out, fmt.Errorf("GraphQL errors: %s", string(blob))
		}

		out.Nodes = append(out.Nodes, resp.Data.Node.Comments.Nodes...)
		out.TotalCount = resp.Data.Node.Comments.TotalCount
		out.PageInfo = resp.Data.Node.Comments.PageInfo
		after = out.PageInfo.EndCursor
		existing.PageInfo = out.PageInfo
	}

	return out, nil
}

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
		loc := fmtLoc(c.Path, c.StartLine, c.Line)

		out = append(out, fmt.Sprintf("## %s", loc))

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

func fetchUnresolvedThreadIDs(owner, repo string, prNumber int) ([]string, error) {
	var ids []string
	var after *string

	for {
		cmd := []string{
			"gh", "api", "graphql",
			"-F", fmt.Sprintf("owner=%s", owner),
			"-F", fmt.Sprintf("name=%s", repo),
			"-F", fmt.Sprintf("number=%d", prNumber),
		}

		if after != nil {
			cmd = append(cmd, "-F", fmt.Sprintf("after=%s", *after))
		}

		cmd = append(cmd, "-f", fmt.Sprintf("query=%s", unresolvedThreadsQuery))

		var resp struct {
			Data struct {
				Repository struct {
					PullRequest struct {
						ReviewThreads struct {
							TotalCount int `json:"totalCount"`
							Nodes      []struct {
								ID         string `json:"id"`
								IsResolved bool   `json:"isResolved"`
							} `json:"nodes"`
							PageInfo pageInfo `json:"pageInfo"`
						} `json:"reviewThreads"`
					} `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
			Errors []graphQLError `json:"errors"`
		}

		if err := ghJSON(cmd, &resp); err != nil {
			return nil, err
		}

		if len(resp.Errors) > 0 {
			blob, _ := json.Marshal(resp.Errors)
			return nil, fmt.Errorf("GraphQL errors: %s", string(blob))
		}

		pull := resp.Data.Repository.PullRequest

		if ids == nil {
			estimated := pull.ReviewThreads.TotalCount
			if estimated <= 0 {
				estimated = len(pull.ReviewThreads.Nodes)
			}
			if estimated > 0 {
				ids = make([]string, 0, estimated)
			}
		}

		for _, n := range pull.ReviewThreads.Nodes {
			if !n.IsResolved && n.ID != "" {
				ids = append(ids, n.ID)
			}
		}

		if pull.ReviewThreads.PageInfo.HasNextPage && pull.ReviewThreads.PageInfo.EndCursor != "" {
			cursor := pull.ReviewThreads.PageInfo.EndCursor
			after = &cursor
			continue
		}

		break
	}

	return ids, nil
}

type reviewSummary struct {
	TotalCount int
	Latest     *prReview
}

func fetchReviewSummary(owner, repo string, prNumber int) (*reviewSummary, error) {
	query := `query($owner: String!, $repo: String!, $number: Int!) {
		repository(owner: $owner, name: $repo) {
			pullRequest(number: $number) {
				number
				reviews { totalCount }
				latestReviews: reviews(last: 1) {
					nodes {
						author { login }
						state
						body
					}
				}
			}
		}
	}`

	cmd := []string{
		"gh", "api", "graphql",
		"-f", fmt.Sprintf("query=%s", query),
		"-F", fmt.Sprintf("owner=%s", owner),
		"-F", fmt.Sprintf("repo=%s", repo),
		"-F", fmt.Sprintf("number=%d", prNumber),
	}

	var resp struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					Number int `json:"number"`
					Reviews struct {
						TotalCount int `json:"totalCount"`
					} `json:"reviews"`
					LatestReviews struct {
						Nodes []struct {
							Author *struct {
								Login string `json:"login"`
							} `json:"author"`
							State string `json:"state"`
							Body  string `json:"body"`
						} `json:"nodes"`
					} `json:"latestReviews"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}

	if err := ghJSON(cmd, &resp); err != nil {
		return nil, err
	}

	if len(resp.Errors) > 0 {
		blob, _ := json.Marshal(resp.Errors)
		return nil, fmt.Errorf("GraphQL errors: %s", string(blob))
	}

	pr := resp.Data.Repository.PullRequest
	if pr == nil {
		return nil, fmt.Errorf("pull request #%d in %s/%s not found or GraphQL query returned no data; verify the PR number and that you have access to the repository", prNumber, owner, repo)
	}

	summary := &reviewSummary{
		TotalCount: pr.Reviews.TotalCount,
	}

	if nodes := pr.LatestReviews.Nodes; len(nodes) > 0 {
		n := nodes[0]
		review := &prReview{
			State: n.State,
			Body:  n.Body,
		}
		if n.Author != nil {
			review.User = &user{Login: n.Author.Login}
		}
		summary.Latest = review
	}

	return summary, nil
}

func runWait(owner, repo string, opts waitOptions) error {
	prNumber, err := resolvePRNumber(opts.prNumber)
	if err != nil {
		return err
	}

	interval := time.Duration(opts.interval) * time.Second
	deadline := time.Now().Add(time.Duration(opts.timeout) * time.Second)

	var initial *reviewSummary
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			fmt.Fprintf(os.Stderr, "\nTimed out after %ds, no new review.\n", opts.timeout)
			return errTimeout
		}

		initial, err = fetchReviewSummary(owner, repo, prNumber)
		if err == nil {
			break
		}

		fmt.Fprintf(os.Stderr, "Warning: failed to fetch reviews: %v\n", err)

		sleep := interval
		if remaining < sleep {
			sleep = remaining
		}
		time.Sleep(sleep)
	}

	initialCount := initial.TotalCount

	fmt.Fprintf(os.Stderr, "Watching PR #%d in %s/%s (current reviews: %d)\n", prNumber, owner, repo, initialCount)
	fmt.Fprintf(os.Stderr, "Checking every %ds, timeout in %ds... (Ctrl+C to stop)\n", opts.interval, opts.timeout)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			fmt.Fprintf(os.Stderr, "\nTimed out after %ds, no new review.\n", opts.timeout)
			return errTimeout
		}

		sleep := interval
		if remaining < sleep {
			sleep = remaining
		}
		time.Sleep(sleep)

		if time.Until(deadline) <= 0 {
			fmt.Fprintf(os.Stderr, "\nTimed out after %ds, no new review.\n", opts.timeout)
			return errTimeout
		}

		summary, err := fetchReviewSummary(owner, repo, prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nWarning: failed to fetch reviews: %v\n", err)
			continue
		}

		if summary.TotalCount > initialCount {
			fmt.Fprintf(os.Stderr, "\n")
			fmt.Println("New review detected!")
			if summary.Latest != nil {
				login := "unknown"
				if summary.Latest.User != nil && summary.Latest.User.Login != "" {
					login = summary.Latest.User.Login
				}
				fmt.Printf("%s — %s\n", login, summary.Latest.State)
				if body := strings.TrimSpace(summary.Latest.Body); body != "" {
					fmt.Println(body)
				}
			}
			return nil
		}

		fmt.Fprintf(os.Stderr, ".")
	}
}

func resolveThread(threadID string) error {
	cmd := []string{
		"gh", "api", "graphql",
		"-f", fmt.Sprintf("query=%s", resolveThreadMutation),
		"-F", fmt.Sprintf("id=%s", threadID),
	}

	var resp struct {
		Data struct {
			ResolveReviewThread struct {
				Thread struct {
					IsResolved bool `json:"isResolved"`
				} `json:"thread"`
			} `json:"resolveReviewThread"`
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

	if !resp.Data.ResolveReviewThread.Thread.IsResolved {
		return errors.New("failed to resolve thread (no confirmation from API)")
	}

	return nil
}

func resolveAllThreads(owner, repo string, prNumber int) (int, error) {
	ids, err := fetchUnresolvedThreadIDs(owner, repo, prNumber)
	if err != nil {
		return 0, err
	}

	if len(ids) == 0 {
		return 0, nil
	}

	// Use concurrency to speed up resolution; limit parallelism to avoid rate limits
	const maxConcurrent = 10
	sem := make(chan struct{}, maxConcurrent)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	resolved := 0

	for _, id := range ids {
		wg.Add(1)
		go func(threadID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := resolveThread(threadID); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("resolved %d threads before failure; failed to resolve thread %s: %w", resolved, threadID, err)
				}
				mu.Unlock()
				return
			}

			mu.Lock()
			resolved++
			mu.Unlock()
		}(id)
	}

	wg.Wait()

	if firstErr != nil {
		return resolved, firstErr
	}

	return resolved, nil
}

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

func validateReviewSubmission(sub reviewSubmission, pending bool) error {
	if pending {
		return nil
	}
	switch sub.Event {
	case "APPROVE":
		// body and comments are both optional
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

func submitReview(owner, repo string, prNumber int, sub reviewSubmission, pending bool) (string, string, error) {
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
	// When the user wants the review submitted, we still create it as pending
	// first, attach the file-level threads, then submit.
	initialPending := pending || hasFileComments

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

	if hasFileComments && !pending {
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

func parsePRArg(args []string) (*int, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("unexpected arguments: %v", args[1:])
	}
	if len(args) == 0 {
		return nil, nil
	}
	prNumber, err := strconv.Atoi(args[0])
	if err != nil {
		return nil, fmt.Errorf("invalid PR number: %q", args[0])
	}
	return &prNumber, nil
}

const usageMessage = "usage: gh-prr <export|resolve|pending|wait|submit|submit-pending> [args]"

func newFlagSet(name string) (*flag.FlagSet, *bytes.Buffer) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	return fs, &buf
}

func parseFlagSet(fs *flag.FlagSet, buf *bytes.Buffer, args []string) error {
	if err := fs.Parse(args); err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

func parseArgs() (parsedArgs, error) {
	var p parsedArgs

	if len(os.Args) < 2 {
		return p, errors.New(usageMessage)
	}

	switch os.Args[1] {
	case string(cmdExport):
		fs, buf := newFlagSet("export")

		fs.IntVar(&p.export.ctx, "c", 3, "Number of lines to keep from the start/end of each diff hunk (alias: -context).")
		fs.IntVar(&p.export.ctx, "context", 3, "Alias of -c for specifying diff context lines.")
		fs.BoolVar(&p.export.unresolvedOnly, "unresolved-only", false, "Show only unresolved threads.")

		if err := parseFlagSet(fs, buf, os.Args[2:]); err != nil {
			return p, err
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return p, err
		}
		p.export.prNumber = prArg

		if p.export.ctx < 1 {
			p.export.ctx = 1
		}
		p.cmd = cmdExport
		return p, nil

	case string(cmdResolve):
		fs, buf := newFlagSet("resolve")

		if err := parseFlagSet(fs, buf, os.Args[2:]); err != nil {
			return p, err
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return p, err
		}
		p.resolve.prNumber = prArg

		p.cmd = cmdResolve
		return p, nil

	case string(cmdPending):
		fs, buf := newFlagSet("pending")

		fs.IntVar(&p.pending.ctx, "c", 3, "Number of lines to keep from the start/end of each diff hunk (alias: -context).")
		fs.IntVar(&p.pending.ctx, "context", 3, "Alias of -c for specifying diff context lines.")

		if err := parseFlagSet(fs, buf, os.Args[2:]); err != nil {
			return p, err
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return p, err
		}
		p.pending.prNumber = prArg

		if p.pending.ctx < 1 {
			p.pending.ctx = 1
		}
		p.cmd = cmdPending
		return p, nil

	case string(cmdWait):
		fs, buf := newFlagSet("wait")

		fs.IntVar(&p.wait.interval, "i", 30, "Polling interval in seconds.")
		fs.IntVar(&p.wait.interval, "interval", 30, "Alias for -i.")
		fs.IntVar(&p.wait.timeout, "t", 900, "Timeout in seconds (default 900 = 15 minutes).")
		fs.IntVar(&p.wait.timeout, "timeout", 900, "Alias for -t.")

		if err := parseFlagSet(fs, buf, os.Args[2:]); err != nil {
			return p, err
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return p, err
		}
		p.wait.prNumber = prArg

		if p.wait.interval < 1 {
			p.wait.interval = 1
		}
		if p.wait.timeout < 1 {
			p.wait.timeout = 1
		}
		p.cmd = cmdWait
		return p, nil

	case string(cmdSubmit):
		fs, buf := newFlagSet("submit")

		fs.StringVar(&p.submit.file, "f", "", "Path to the review Markdown file. Use - for stdin. (alias: -file)")
		fs.StringVar(&p.submit.file, "file", "", "Alias for -f.")
		fs.BoolVar(&p.submit.pending, "pending", false, "Submit as a pending (draft) review without finalizing.")

		if err := parseFlagSet(fs, buf, os.Args[2:]); err != nil {
			return p, err
		}

		if strings.TrimSpace(p.submit.file) == "" {
			return p, errors.New("submit requires -f <file> (use - for stdin)")
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return p, err
		}
		p.submit.prNumber = prArg

		p.cmd = cmdSubmit
		return p, nil

	case string(cmdSubmitPending):
		fs, buf := newFlagSet("submit-pending")

		fs.StringVar(&p.submitPending.event, "e", "COMMENT", "Event to submit with: APPROVE, REQUEST_CHANGES, or COMMENT. (alias: -event)")
		fs.StringVar(&p.submitPending.event, "event", "COMMENT", "Alias for -e.")

		if err := parseFlagSet(fs, buf, os.Args[2:]); err != nil {
			return p, err
		}

		ev := strings.ToUpper(strings.TrimSpace(p.submitPending.event))
		switch ev {
		case "APPROVE", "REQUEST_CHANGES", "COMMENT":
			p.submitPending.event = ev
		default:
			return p, fmt.Errorf("invalid event %q (expected APPROVE, REQUEST_CHANGES, or COMMENT)", p.submitPending.event)
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return p, err
		}
		p.submitPending.prNumber = prArg

		p.cmd = cmdSubmitPending
		return p, nil

	case "-h", "--help":
		return p, errors.New(usageMessage)
	default:
		return p, fmt.Errorf("unknown command %q (use export, resolve, pending, wait, submit, or submit-pending)", os.Args[1])
	}
}

func main() {
	args, err := parseArgs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	owner, repo, err := getOwnerRepo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	switch args.cmd {
	case cmdExport:
		prNumber, err := resolvePRNumber(args.export.prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		prInfo, threads, err := fetchThreads(owner, repo, prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		fmt.Print(renderMarkdown(prInfo, threads, args.export.ctx, args.export.unresolvedOnly))

	case cmdResolve:
		prNumber, err := resolvePRNumber(args.resolve.prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		count, err := resolveAllThreads(owner, repo, prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		if count == 0 {
			fmt.Println("No unresolved review threads.")
		} else {
			fmt.Printf("Resolved %d thread(s).\n", count)
		}

	case cmdPending:
		prNumber, err := resolvePRNumber(args.pending.prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		prInfo, review, err := fetchPendingReview(owner, repo, prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		fmt.Print(renderPendingMarkdown(prInfo, review, args.pending.ctx))

	case cmdWait:
		if err := runWait(owner, repo, args.wait); err != nil {
			if errors.Is(err, errTimeout) {
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case cmdSubmit:
		content, err := readReviewFile(args.submit.file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		sub, err := parseReviewMarkdown(content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		if err := validateReviewSubmission(sub, args.submit.pending); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		if args.submit.pending && (sub.Event == "APPROVE" || sub.Event == "REQUEST_CHANGES") {
			fmt.Fprintf(os.Stderr, "warning: --pending is set; front matter event %q will be ignored (use `gh-prr submit-pending -e %s` after to finalize)\n", sub.Event, sub.Event)
		}

		prNumber, err := resolvePRNumber(args.submit.prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		url, state, err := submitReview(owner, repo, prNumber, sub, args.submit.pending)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		if args.submit.pending {
			fmt.Printf("Created pending review (%d inline comment(s)). State: %s\n", len(sub.Comments), state)
		} else {
			fmt.Printf("Submitted review (%d inline comment(s)). State: %s\n", len(sub.Comments), state)
		}
		if url != "" {
			fmt.Println(url)
		}

	case cmdSubmitPending:
		prNumber, err := resolvePRNumber(args.submitPending.prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		_, review, err := fetchPendingReview(owner, repo, prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if review == nil {
			fmt.Fprintln(os.Stderr, "error: no pending review found")
			os.Exit(1)
		}

		url, state, err := submitPendingReview(review.ID, args.submitPending.event)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Submitted pending review. State: %s\n", state)
		if url != "" {
			fmt.Println(url)
		}
	}
}
