// Package hostingtest is a conformance suite that every hosting.Provider must
// pass, so the interface means the same thing for every provider.
package hostingtest

import (
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

// Subject is a provider together with the controls that change what is behind
// it. A nil control makes the subtests that need it skip.
type Subject struct {
	Provider hosting.Provider

	// Merge merges the pull request; Decline closes it without merging.
	Merge   func(t testing.TB, number int)
	Decline func(t testing.TB, number int)

	// SetHead sets the commit the pull request's head branch is at on the
	// service, which the provider reports as HeadCommit.
	SetHead func(t testing.TB, number int, sha string)

	// AddReview adds a review (Kind review, State CHANGES_REQUESTED or APPROVED).
	// A provider that cannot carry a body ignores it.
	AddReview func(t testing.TB, number int, f hosting.Feedback)
	// AddInline adds an inline comment (Path, Line, Body, Author).
	AddInline func(t testing.TB, number int, f hosting.Feedback)
	// AddComment adds a general comment (Body, Author).
	AddComment func(t testing.TB, number int, f hosting.Feedback)

	// RejectCredentials, RateLimit and BringDown make the backing service
	// answer as an unauthorized, rate-limited or unavailable service.
	RejectCredentials func(t testing.TB)
	RateLimit         func(t testing.TB, resetAt time.Time)
	BringDown         func(t testing.TB)

	// Pause, when not zero, is how long to wait between feedback additions so
	// that the service stamps them with increasing times. Subtests that
	// assert ordering are skipped under go test -short when it is set.
	Pause time.Duration
}

// MissingControls names the controls that are not set, so a test can insist
// that no conformance subtest is skipped for want of one.
func (s Subject) MissingControls() []string {
	var out []string
	for _, c := range []string{ctlMerge, ctlDecline, ctlSetHead, ctlReview, ctlInline, ctlComment, ctlReject, ctlRateLimit, ctlBringDown} {
		if missing(s, c) {
			out = append(out, c)
		}
	}
	return out
}

// runT is the part of *testing.T the suite needs, so that tests of the suite
// can run it against a recorder.
type runT interface {
	testing.TB
	Run(name string, f func(t runT)) bool
}

type stdT struct{ *testing.T }

func (s stdT) Run(name string, f func(t runT)) bool {
	return s.T.Run(name, func(t *testing.T) { f(stdT{t}) })
}
