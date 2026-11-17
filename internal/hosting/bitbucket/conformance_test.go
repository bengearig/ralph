package bitbucket

import (
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/hosting/bitbucket/bitbuckettest"
	"github.com/bengearig/ralph/internal/hosting/hostingtest"
)

func TestBitbucketConformance(t *testing.T) {
	for _, auth := range []struct{ name, user string }{
		{"Bearer", ""},
		{"Basic", "person@example.com"},
	} {
		t.Run(auth.name, func(t *testing.T) {
			hostingtest.Run(t, func(t *testing.T) hostingtest.Subject {
				srv := bitbuckettest.New(t, bitbuckettest.Config{User: auth.user, PageSize: 3})
				p, err := New(Config{
					APIURL: srv.URL(), Workspace: srv.Workspace(), Repo: srv.Repo(), User: srv.User(),
					Token: func() (string, error) { return srv.Token(), nil }, Timeout: 5 * time.Second,
				})
				if err != nil {
					t.Fatal(err)
				}
				review := func(_ testing.TB, n int, f hosting.Feedback) {
					state := "approved"
					if f.State == hosting.ReviewChangesRequested {
						state = "changes_requested"
					}
					srv.AddReview(n, bitbuckettest.Review{Author: f.Author, State: state})
				}
				comment := func(add func(int, bitbuckettest.Comment) int) func(testing.TB, int, hosting.Feedback) {
					return func(_ testing.TB, n int, f hosting.Feedback) {
						add(n, bitbuckettest.Comment{Author: f.Author, Body: f.Body, Path: f.Path, Line: f.Line})
					}
				}
				s := hostingtest.Subject{
					Provider:          p,
					Merge:             func(_ testing.TB, n int) { srv.Merge(n) },
					Decline:           func(_ testing.TB, n int) { srv.Decline(n) },
					SetHead:           func(_ testing.TB, n int, sha string) { srv.SetHead(n, sha) },
					AddReview:         review,
					AddInline:         comment(srv.AddInline),
					AddComment:        comment(srv.AddComment),
					RejectCredentials: func(testing.TB) { srv.RejectCredentials() },
					RateLimit:         func(_ testing.TB, at time.Time) { srv.RateLimit(at) },
					BringDown:         func(testing.TB) { srv.BringDown() },
				}
				if missing := s.MissingControls(); len(missing) > 0 {
					t.Fatalf("controls not wired: %v", missing)
				}
				return s
			})
		})
	}
}
