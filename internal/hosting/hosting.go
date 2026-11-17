// Package hosting defines the pull request provider interface, its types and
// errors, and remote detection. Implementations, local or reached through a
// server, must return this package's errors so callers can test them with
// errors.Is.
package hosting

import (
	"context"
	"time"
)

// PRState is the lifecycle state of a pull request.
type PRState string

const (
	StateOpen PRState = "open"
	// StateClosed means closed without merging.
	StateClosed PRState = "closed"
	StateMerged PRState = "merged"
)

// PullRequest is a pull request as the provider reports it.
type PullRequest struct {
	Number      int
	URL         string
	Title       string
	Body        string
	Head        string
	Base        string
	Draft       bool
	State       PRState
	MergeCommit string
	// HeadCommit is the full hash of the head branch's tip as the host last
	// knew it for the pull request (for a merged pull request, the commit
	// that was merged). It is empty when the provider does not report one.
	HeadCommit string
}

// NewPullRequest is what is needed to open a pull request.
type NewPullRequest struct {
	Head  string
	Base  string
	Title string
	Body  string
	Draft bool
}

// FeedbackKind says where a piece of feedback was left.
type FeedbackKind string

const (
	KindReview  FeedbackKind = "review"
	KindInline  FeedbackKind = "inline"
	KindComment FeedbackKind = "comment"
)

// ReviewState is the verdict of a review; it is empty for other feedback.
type ReviewState string

const (
	ReviewChangesRequested ReviewState = "CHANGES_REQUESTED"
	ReviewApproved         ReviewState = "APPROVED"
	ReviewCommented        ReviewState = "COMMENTED"
)

// Feedback is one review, inline comment or general comment.
type Feedback struct {
	ID        string
	Kind      FeedbackKind
	Author    string
	Body      string
	Path      string
	Line      int
	State     ReviewState
	CreatedAt time.Time
	URL       string
}

// FeedbackOptions narrows ReviewFeedback.
type FeedbackOptions struct {
	// Since, if not zero, excludes feedback created before it.
	Since time.Time
}

// Provider is a pull request host.
type Provider interface {
	// CreatePullRequest opens a pull request, returning ErrConflict if one is already open for the head.
	CreatePullRequest(ctx context.Context, pr NewPullRequest) (PullRequest, error)
	// FindPullRequest returns the pull request for a head branch, preferring an open one, and false if none exists.
	FindPullRequest(ctx context.Context, head string) (PullRequest, bool, error)
	// GetPullRequest returns the pull request with the given number, or ErrNotFound.
	GetPullRequest(ctx context.Context, number int) (PullRequest, error)
	// ReviewFeedback returns the feedback on a pull request, oldest first.
	ReviewFeedback(ctx context.Context, number int, opts FeedbackOptions) ([]Feedback, error)
}
