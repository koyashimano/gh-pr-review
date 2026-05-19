package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

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

const selfReviewerToken = "@me"

func isSelfReviewer(s string) bool {
	return strings.EqualFold(strings.TrimSpace(s), selfReviewerToken)
}

func containsSelfReviewer(reviewers []string) bool {
	for _, r := range reviewers {
		if isSelfReviewer(r) {
			return true
		}
	}
	return false
}

func substituteSelfReviewer(reviewers []string, login string) []string {
	out := make([]string, 0, len(reviewers))
	for _, r := range reviewers {
		if isSelfReviewer(r) {
			out = append(out, login)
		} else {
			out = append(out, r)
		}
	}
	return out
}

func fetchViewerLogin() (string, error) {
	cmd := []string{
		"gh", "api", "graphql",
		"-f", fmt.Sprintf("query=%s", viewerLoginQuery),
	}

	var resp struct {
		Data struct {
			Viewer struct {
				Login string `json:"login"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}

	if err := ghJSON(cmd, &resp); err != nil {
		return "", err
	}
	if len(resp.Errors) > 0 {
		blob, _ := json.Marshal(resp.Errors)
		return "", fmt.Errorf("GraphQL errors: %s", string(blob))
	}
	return resp.Data.Viewer.Login, nil
}

func expandSelfReviewer(reviewers []string) ([]string, error) {
	if !containsSelfReviewer(reviewers) {
		return reviewers, nil
	}
	login, err := fetchViewerLogin()
	if err != nil {
		return nil, fmt.Errorf("resolve @me: %w", err)
	}
	if login == "" {
		return nil, errors.New("could not determine current user for @me")
	}
	return substituteSelfReviewer(reviewers, login), nil
}

func normalizeReviewers(reviewers []string) map[string]struct{} {
	if len(reviewers) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(reviewers))
	for _, r := range reviewers {
		t := strings.ToLower(strings.TrimSpace(r))
		if t == "" {
			continue
		}
		set[t] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func threadMatchesReviewer(authorLogin string, reviewerSet map[string]struct{}) bool {
	if len(reviewerSet) == 0 {
		return true
	}
	if authorLogin == "" {
		return false
	}
	_, ok := reviewerSet[strings.ToLower(authorLogin)]
	return ok
}

func fetchUnresolvedThreadIDs(owner, repo string, prNumber int, reviewers []string) ([]string, error) {
	reviewerSet := normalizeReviewers(reviewers)

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
								Comments   struct {
									Nodes []struct {
										State  string `json:"state"`
										Author *user  `json:"author"`
									} `json:"nodes"`
								} `json:"comments"`
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
			if n.IsResolved || n.ID == "" {
				continue
			}
			if len(n.Comments.Nodes) == 0 {
				continue
			}
			first := n.Comments.Nodes[0]
			if first.State == "PENDING" {
				continue
			}
			var authorLogin string
			if first.Author != nil {
				authorLogin = first.Author.Login
			}
			if !threadMatchesReviewer(authorLogin, reviewerSet) {
				continue
			}
			ids = append(ids, n.ID)
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

func resolveAllThreads(owner, repo string, prNumber int, reviewers []string) (int, error) {
	ids, err := fetchUnresolvedThreadIDs(owner, repo, prNumber, reviewers)
	if err != nil {
		return 0, err
	}

	if len(ids) == 0 {
		return 0, nil
	}

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

func runExport(owner, repo string, opts exportOptions) error {
	prNumber, err := resolvePRNumber(opts.prNumber)
	if err != nil {
		return err
	}

	prInfo, threads, err := fetchThreads(owner, repo, prNumber)
	if err != nil {
		return err
	}

	fmt.Print(renderMarkdown(prInfo, threads, opts.ctx, opts.includeResolved))
	return nil
}

func runResolve(owner, repo string, opts resolveOptions) error {
	prNumber, err := resolvePRNumber(opts.prNumber)
	if err != nil {
		return err
	}

	reviewers, err := expandSelfReviewer(opts.reviewers)
	if err != nil {
		return err
	}

	count, err := resolveAllThreads(owner, repo, prNumber, reviewers)
	if err != nil {
		return err
	}

	if count == 0 {
		if len(reviewers) > 0 {
			fmt.Fprintf(os.Stdout, "No unresolved review threads from %s.\n", strings.Join(reviewers, ", "))
		} else {
			fmt.Fprintln(os.Stdout, "No unresolved review threads.")
		}
	} else {
		if len(reviewers) > 0 {
			fmt.Fprintf(os.Stdout, "Resolved %d thread(s) from %s.\n", count, strings.Join(reviewers, ", "))
		} else {
			fmt.Fprintf(os.Stdout, "Resolved %d thread(s).\n", count)
		}
	}
	return nil
}
