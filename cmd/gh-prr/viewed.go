package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
)

type prFile struct {
	Path              string `json:"path"`
	ViewerViewedState string `json:"viewerViewedState"`
}

type prFilesResponse struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				ID     string `json:"id"`
				Number int    `json:"number"`
				Title  string `json:"title"`
				URL    string `json:"url"`
				Files  struct {
					TotalCount int      `json:"totalCount"`
					Nodes      []prFile `json:"nodes"`
					PageInfo   pageInfo `json:"pageInfo"`
				} `json:"files"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

func fetchPRFiles(owner, repo string, prNumber int) (string, []prFile, error) {
	var prID string
	var files []prFile
	var after *string

	for {
		cmd := []string{
			"gh", "api", "graphql",
			"-f", fmt.Sprintf("owner=%s", owner),
			"-f", fmt.Sprintf("name=%s", repo),
			"-F", fmt.Sprintf("number=%d", prNumber),
		}
		if after != nil {
			cmd = append(cmd, "-f", fmt.Sprintf("after=%s", *after))
		}
		cmd = append(cmd, "-f", fmt.Sprintf("query=%s", prFilesQuery))

		var resp prFilesResponse
		if err := ghJSON(cmd, &resp); err != nil {
			return "", nil, err
		}
		if len(resp.Errors) > 0 {
			blob, _ := json.Marshal(resp.Errors)
			return "", nil, fmt.Errorf("GraphQL errors: %s", string(blob))
		}

		pr := resp.Data.Repository.PullRequest
		if pr.ID == "" {
			return "", nil, fmt.Errorf("pull request #%d in %s/%s not found or GraphQL query returned no data; verify the PR number and that you have access to the repository", prNumber, owner, repo)
		}
		prID = pr.ID

		if files == nil {
			estimated := pr.Files.TotalCount
			if estimated <= 0 {
				estimated = len(pr.Files.Nodes)
			}
			if estimated > 0 {
				files = make([]prFile, 0, estimated)
			}
		}
		files = append(files, pr.Files.Nodes...)

		if pr.Files.PageInfo.HasNextPage && pr.Files.PageInfo.EndCursor != "" {
			cursor := pr.Files.PageInfo.EndCursor
			after = &cursor
			continue
		}
		break
	}

	return prID, files, nil
}

// matchPathGlob reports whether name matches the glob pattern.
//
// Both pattern and name are split on "/" into segments. Within each segment,
// only *, ? and \c are interpreted; [...] is treated as literal text so that
// real-world paths such as "pages/[id].tsx" match as-is. * does not cross
// segment boundaries because segments are matched independently. A literal
// "**" as a whole segment matches zero or more path segments, including "/".
func matchPathGlob(pattern, name string) (bool, error) {
	pat := strings.Split(pattern, "/")
	nm := strings.Split(name, "/")
	return matchGlobSegments(pat, nm)
}

// escapeBrackets escapes "[" and "]" so path.Match treats them literally
// rather than as a character class.
func escapeBrackets(s string) string {
	if !strings.ContainsAny(s, "[]") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 2)
	for _, r := range s {
		if r == '[' || r == ']' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func matchGlobSegments(pat, name []string) (bool, error) {
	for {
		if len(pat) == 0 {
			return len(name) == 0, nil
		}
		if pat[0] == "**" {
			for i := 0; i <= len(name); i++ {
				ok, err := matchGlobSegments(pat[1:], name[i:])
				if err != nil {
					return false, err
				}
				if ok {
					return true, nil
				}
			}
			return false, nil
		}
		if len(name) == 0 {
			return false, nil
		}
		ok, err := path.Match(escapeBrackets(pat[0]), name[0])
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		pat = pat[1:]
		name = name[1:]
	}
}

func validateGlobPattern(pattern string) error {
	if pattern == "" {
		return errors.New("empty pattern")
	}
	for _, seg := range strings.Split(pattern, "/") {
		if seg == "" {
			return fmt.Errorf("empty path segment in pattern %q", pattern)
		}
		if seg == "**" {
			continue
		}
		if _, err := path.Match(escapeBrackets(seg), ""); err != nil {
			return fmt.Errorf("invalid pattern %q: %w", pattern, err)
		}
	}
	return nil
}

func setFileViewed(prID, filePath string, unmark bool) error {
	query := markFileAsViewedMutation
	if unmark {
		query = unmarkFileAsViewedMutation
	}
	cmd := []string{
		"gh", "api", "graphql",
		"-f", fmt.Sprintf("pullRequestId=%s", prID),
		"-f", fmt.Sprintf("path=%s", filePath),
		"-f", fmt.Sprintf("query=%s", query),
	}

	var resp struct {
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

// isAlreadyTarget reports whether the file's current viewed state already
// matches what the caller is trying to achieve, so the mutation can be skipped.
//
// On --unmark, both UNVIEWED and DISMISSED are treated as "already unviewed":
// DISMISSED means GitHub auto-cleared the Viewed flag because the file
// changed, so re-asserting UNVIEWED would be a no-op API call.
func isAlreadyTarget(state string, unmark bool) bool {
	if unmark {
		return state == "UNVIEWED" || state == "DISMISSED"
	}
	return state == "VIEWED"
}

func runViewed(owner, repo string, opts viewedOptions) error {
	for _, p := range opts.patterns {
		if err := validateGlobPattern(p); err != nil {
			return err
		}
	}

	prNumber, err := resolvePRNumber(opts.prNumber)
	if err != nil {
		return err
	}

	prID, files, err := fetchPRFiles(owner, repo, prNumber)
	if err != nil {
		return err
	}

	var matched []prFile
	skipped := 0
	for _, f := range files {
		var hit bool
		for _, pat := range opts.patterns {
			ok, err := matchPathGlob(pat, f.Path)
			if err != nil {
				return fmt.Errorf("invalid pattern %q: %w", pat, err)
			}
			if ok {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		if isAlreadyTarget(f.ViewerViewedState, opts.unmark) {
			skipped++
			continue
		}
		matched = append(matched, f)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Path < matched[j].Path })

	action := "mark"
	pastAction := "Marked"
	stateLower := "viewed"
	if opts.unmark {
		action = "unmark"
		pastAction = "Unmarked"
		stateLower = "unviewed"
	}

	if len(matched) == 0 && skipped == 0 {
		fmt.Fprintln(os.Stdout, "No files matched the given pattern(s).")
		return nil
	}

	if opts.dryRun {
		for _, f := range matched {
			fmt.Fprintf(os.Stdout, "would %s: %s\n", action, f.Path)
		}
		fmt.Fprintf(os.Stdout, "dry run: %d file(s) would be %s, %d already %s.\n", len(matched), stateLower, skipped, stateLower)
		return nil
	}

	if len(matched) == 0 {
		fmt.Fprintf(os.Stdout, "All %d matching file(s) are already %s.\n", skipped, stateLower)
		return nil
	}

	const maxConcurrent = 10
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	var firstErrPath string
	done := 0

	for _, f := range matched {
		wg.Add(1)
		go func(fp string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := setFileViewed(prID, fp, opts.unmark); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					firstErrPath = fp
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			done++
			mu.Unlock()
			fmt.Fprintf(os.Stdout, "%s: %s\n", strings.ToLower(pastAction), fp)
		}(f.Path)
	}
	wg.Wait()

	if firstErr != nil {
		return fmt.Errorf("%s %d file(s); first failure on %s: %w", strings.ToLower(pastAction), done, firstErrPath, firstErr)
	}

	suffix := ""
	if skipped > 0 {
		suffix = fmt.Sprintf(" (%d already %s)", skipped, stateLower)
	}
	fmt.Fprintf(os.Stdout, "%s %d file(s)%s.\n", pastAction, done, suffix)
	return nil
}
