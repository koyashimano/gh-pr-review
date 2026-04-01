package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type command string

const (
	cmdExport  command = "export"
	cmdResolve command = "resolve"
	cmdPending command = "pending"
	cmdWait    command = "wait"
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

const pendingReviewQuery = `
query($owner: String!, $name: String!, $number: Int!) {
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

	reviews := pr.Reviews.Nodes
	if len(reviews) == 0 {
		return prInfo, nil, nil
	}

	review := reviews[0]

	if review.Comments.PageInfo.HasNextPage {
		fullComments, err := fetchAllPendingReviewComments(review.ID, review.Comments)
		if err != nil {
			return prInfo, nil, err
		}
		review.Comments = fullComments
	}

	return prInfo, &review, nil
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
							Author struct {
								Login string `json:"login"`
							} `json:"author"`
							State string `json:"state"`
							Body  string `json:"body"`
						} `json:"nodes"`
					} `json:"latestReviews"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}

	if err := ghJSON(cmd, &resp); err != nil {
		return nil, err
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
		summary.Latest = &prReview{
			User:  &user{Login: n.Author.Login},
			State: n.State,
			Body:  n.Body,
		}
	}

	return summary, nil
}

func runWait(owner, repo string, opts waitOptions) error {
	prNumber, err := resolvePRNumber(opts.prNumber)
	if err != nil {
		return err
	}

	initial, err := fetchReviewSummary(owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("failed to fetch initial reviews: %w", err)
	}
	initialCount := initial.TotalCount

	interval := time.Duration(opts.interval) * time.Second
	deadline := time.Now().Add(time.Duration(opts.timeout) * time.Second)

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

func parseArgs() (command, exportOptions, resolveOptions, pendingOptions, waitOptions, error) {
	var exp exportOptions
	var res resolveOptions

	if len(os.Args) < 2 {
		return "", exp, res, pendingOptions{}, waitOptions{}, errors.New("command required: export, resolve, pending, or wait")
	}

	switch os.Args[1] {
	case string(cmdExport):
		fs := flag.NewFlagSet("export", flag.ContinueOnError)
		var buf bytes.Buffer
		fs.SetOutput(&buf)

		fs.IntVar(&exp.ctx, "c", 3, "Number of lines to keep from the start/end of each diff hunk (alias: -context).")
		fs.IntVar(&exp.ctx, "context", 3, "Alias of -c for specifying diff context lines.")
		fs.BoolVar(&exp.unresolvedOnly, "unresolved-only", false, "Show only unresolved threads.")

		if err := fs.Parse(os.Args[2:]); err != nil {
			msg := strings.TrimSpace(buf.String())
			if msg == "" {
				return "", exp, res, pendingOptions{}, waitOptions{}, err
			}
			return "", exp, res, pendingOptions{}, waitOptions{}, errors.New(msg)
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return "", exp, res, pendingOptions{}, waitOptions{}, err
		}
		exp.prNumber = prArg

		if exp.ctx < 1 {
			exp.ctx = 1
		}
		return cmdExport, exp, res, pendingOptions{}, waitOptions{}, nil

	case string(cmdResolve):
		fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
		var buf bytes.Buffer
		fs.SetOutput(&buf)

		if err := fs.Parse(os.Args[2:]); err != nil {
			msg := strings.TrimSpace(buf.String())
			if msg == "" {
				return "", exp, res, pendingOptions{}, waitOptions{}, err
			}
			return "", exp, res, pendingOptions{}, waitOptions{}, errors.New(msg)
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return "", exp, res, pendingOptions{}, waitOptions{}, err
		}
		res.prNumber = prArg

		return cmdResolve, exp, res, pendingOptions{}, waitOptions{}, nil

	case string(cmdPending):
		fs := flag.NewFlagSet("pending", flag.ContinueOnError)
		var buf bytes.Buffer
		fs.SetOutput(&buf)

		var pend pendingOptions
		fs.IntVar(&pend.ctx, "c", 3, "Number of lines to keep from the start/end of each diff hunk (alias: -context).")
		fs.IntVar(&pend.ctx, "context", 3, "Alias of -c for specifying diff context lines.")

		if err := fs.Parse(os.Args[2:]); err != nil {
			msg := strings.TrimSpace(buf.String())
			if msg == "" {
				return "", exp, res, pend, waitOptions{}, err
			}
			return "", exp, res, pend, waitOptions{}, errors.New(msg)
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return "", exp, res, pend, waitOptions{}, err
		}
		pend.prNumber = prArg

		if pend.ctx < 1 {
			pend.ctx = 1
		}
		return cmdPending, exp, res, pend, waitOptions{}, nil

	case string(cmdWait):
		fs := flag.NewFlagSet("wait", flag.ContinueOnError)
		var buf bytes.Buffer
		fs.SetOutput(&buf)

		var wait waitOptions
		fs.IntVar(&wait.interval, "i", 30, "Polling interval in seconds.")
		fs.IntVar(&wait.interval, "interval", 30, "Alias for -i.")
		fs.IntVar(&wait.timeout, "t", 900, "Timeout in seconds (default 900 = 15 minutes).")
		fs.IntVar(&wait.timeout, "timeout", 900, "Alias for -t.")

		if err := fs.Parse(os.Args[2:]); err != nil {
			msg := strings.TrimSpace(buf.String())
			if msg == "" {
				return "", exp, res, pendingOptions{}, wait, err
			}
			return "", exp, res, pendingOptions{}, wait, errors.New(msg)
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return "", exp, res, pendingOptions{}, wait, err
		}
		wait.prNumber = prArg

		if wait.interval < 1 {
			wait.interval = 1
		}
		if wait.timeout < 1 {
			wait.timeout = 1
		}
		return cmdWait, exp, res, pendingOptions{}, wait, nil

	case "-h", "--help":
		return "", exp, res, pendingOptions{}, waitOptions{}, errors.New("usage: gh-prr <export|resolve|pending|wait> [args]")
	default:
		return "", exp, res, pendingOptions{}, waitOptions{}, fmt.Errorf("unknown command %q (use export, resolve, pending, or wait)", os.Args[1])
	}
}

func main() {
	cmd, exportOpts, resolveOpts, pendingOpts, waitOpts, err := parseArgs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	owner, repo, err := getOwnerRepo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	switch cmd {
	case cmdExport:
		prNumber, err := resolvePRNumber(exportOpts.prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		prInfo, threads, err := fetchThreads(owner, repo, prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		fmt.Print(renderMarkdown(prInfo, threads, exportOpts.ctx, exportOpts.unresolvedOnly))

	case cmdResolve:
		prNumber, err := resolvePRNumber(resolveOpts.prNumber)
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
		prNumber, err := resolvePRNumber(pendingOpts.prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		prInfo, review, err := fetchPendingReview(owner, repo, prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		fmt.Print(renderPendingMarkdown(prInfo, review, pendingOpts.ctx))

	case cmdWait:
		if err := runWait(owner, repo, waitOpts); err != nil {
			if errors.Is(err, errTimeout) {
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
}
