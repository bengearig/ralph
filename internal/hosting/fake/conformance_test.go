package fake_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/hosting/fake"
	"github.com/bengearig/ralph/internal/hosting/hostingtest"
)

func subjectOf(p *fake.Provider) hostingtest.Subject {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 0
	add := func(prefix string, kind hosting.FeedbackKind) func(testing.TB, int, hosting.Feedback) {
		return func(_ testing.TB, number int, f hosting.Feedback) {
			n++
			f.ID = fmt.Sprintf("%s-%d", prefix, n)
			f.Kind = kind
			f.CreatedAt = base.Add(time.Duration(n) * time.Minute)
			f.URL = fmt.Sprintf("https://fake.example/pull/%d#%s", number, f.ID)
			p.AddFeedback(number, f)
		}
	}
	return hostingtest.Subject{
		Provider:          p,
		Merge:             func(_ testing.TB, number int) { p.Merge(number) },
		Decline:           func(_ testing.TB, number int) { p.Close(number) },
		AddReview:         add("review", hosting.KindReview),
		AddInline:         add("inline", hosting.KindInline),
		AddComment:        add("comment", hosting.KindComment),
		RejectCredentials: func(testing.TB) { p.RejectCredentials() },
		RateLimit:         func(_ testing.TB, resetAt time.Time) { p.RateLimit(resetAt) },
		BringDown:         func(testing.TB) { p.BringDown() },
	}
}

func TestFakeConformance(t *testing.T) {
	hostingtest.Run(t, func(*testing.T) hostingtest.Subject { return subjectOf(fake.New()) })
}
