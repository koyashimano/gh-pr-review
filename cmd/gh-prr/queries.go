package main

const gqlQuery = `
query($owner: String!, $name: String!, $number: Int!, $after: String, $withReviews: Boolean!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      id
      number
      title
      url
      reviews(first: 100) @include(if: $withReviews) {
        totalCount
        nodes {
          id
          url
          state
          body
          submittedAt
          author { login }
        }
        pageInfo { hasNextPage endCursor }
      }
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

const reviewPageQuery = `
query($id: ID!, $after: String) {
  node(id: $id) {
    ... on PullRequest {
      reviews(first: 100, after: $after) {
        totalCount
        nodes {
          id
          url
          state
          body
          submittedAt
          author { login }
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
          comments(first: 1) {
            nodes {
              state
              author { login }
            }
          }
        }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}
`

const viewerLoginQuery = `query { viewer { login } }`

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

const addInlineThreadMutation = `
mutation($id: ID!, $path: String!, $body: String!, $line: Int!, $side: DiffSide!) {
  addPullRequestReviewThread(input: {
    pullRequestReviewId: $id,
    path: $path,
    body: $body,
    line: $line,
    side: $side,
    subjectType: LINE
  }) {
    thread { id }
  }
}
`

const addInlineThreadRangeMutation = `
mutation($id: ID!, $path: String!, $body: String!, $line: Int!, $side: DiffSide!, $startLine: Int!, $startSide: DiffSide!) {
  addPullRequestReviewThread(input: {
    pullRequestReviewId: $id,
    path: $path,
    body: $body,
    line: $line,
    side: $side,
    startLine: $startLine,
    startSide: $startSide,
    subjectType: LINE
  }) {
    thread { id }
  }
}
`

const updatePullRequestReviewBodyMutation = `
mutation($id: ID!, $body: String!) {
  updatePullRequestReview(input: { pullRequestReviewId: $id, body: $body }) {
    pullRequestReview {
      url
    }
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
          url
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

const prFilesQuery = `
query($owner: String!, $name: String!, $number: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      id
      number
      title
      url
      files(first: 100, after: $after) {
        totalCount
        nodes {
          path
          viewerViewedState
        }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}
`

const markFileAsViewedMutation = `
mutation($pullRequestId: ID!, $path: String!) {
  markFileAsViewed(input: {pullRequestId: $pullRequestId, path: $path}) {
    clientMutationId
  }
}
`

const unmarkFileAsViewedMutation = `
mutation($pullRequestId: ID!, $path: String!) {
  unmarkFileAsViewed(input: {pullRequestId: $pullRequestId, path: $path}) {
    clientMutationId
  }
}
`

const reviewSummaryQuery = `query($owner: String!, $repo: String!, $number: Int!) {
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
