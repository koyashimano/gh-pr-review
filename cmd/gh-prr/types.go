package main

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
