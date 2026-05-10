package main

import (
	"encoding/json"
	"fmt"
)

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

func runPending(owner, repo string, opts pendingOptions) error {
	prNumber, err := resolvePRNumber(opts.prNumber)
	if err != nil {
		return err
	}

	prInfo, review, err := fetchPendingReview(owner, repo, prNumber)
	if err != nil {
		return err
	}

	fmt.Print(renderPendingMarkdown(prInfo, review, opts.ctx))
	return nil
}
