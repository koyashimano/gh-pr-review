package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

var errTimeout = errors.New("timeout: no new review detected")

type reviewSummary struct {
	TotalCount int
	Latest     *prReview
}

func fetchReviewSummary(owner, repo string, prNumber int) (*reviewSummary, error) {
	cmd := []string{
		"gh", "api", "graphql",
		"-f", fmt.Sprintf("query=%s", reviewSummaryQuery),
		"-F", fmt.Sprintf("owner=%s", owner),
		"-F", fmt.Sprintf("repo=%s", repo),
		"-F", fmt.Sprintf("number=%d", prNumber),
	}

	var resp struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					Number  int `json:"number"`
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
