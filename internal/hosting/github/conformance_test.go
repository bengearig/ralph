package github

import (
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/hosting/github/githubtest"
	"github.com/bengearig/ralph/internal/hosting/hostingtest"
)

func TestGitHubConformance(t *testing.T) {
	hostingtest.Run(t, func(t *testing.T) hostingtest.Subject {
		srv := githubtest.New(t, githubtest.Config{PageSize: 3})
		p, err := New(Config{
			APIURL: srv.URL(), Owner: srv.Owner(), Repo: srv.Repo(),
			Token: func() (string, error) { return srv.Token(), nil }, Timeout: 5 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		review := func(_ testing.TB, n int, f hosting.Feedback) {
			srv.AddReview(n, githubtest.Review{Author: f.Author, State: string(f.State), Body: f.Body})
		}
		comment := func(add func(int, githubtest.Comment) int) func(testing.TB, int, hosting.Feedback) {
			return func(_ testing.TB, n int, f hosting.Feedback) {
				add(n, githubtest.Comment{Author: f.Author, Body: f.Body, Path: f.Path, Line: f.Line})
			}
		}
		s := hostingtest.Subject{
			Provider:          p,
			Merge:             func(_ testing.TB, n int) { srv.Merge(n) },
			Decline:           func(_ testing.TB, n int) { srv.Decline(n) },
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
}
