package main

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
          comments(first: 1) {
            nodes { state }
          }
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
