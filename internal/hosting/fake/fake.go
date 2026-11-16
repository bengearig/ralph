// Package fake is an in-memory hosting.Provider for tests.
package fake

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

// Call records one operation on the provider.
type Call struct {
	Op     string
	Number int
	Head   string
}

// Provider is an in-memory hosting.Provider.
type Provider struct {
	mu       sync.Mutex
	prs      []hosting.PullRequest
	feedback map[int][]hosting.Feedback
	calls    []Call
	failure  func() error
}

// New returns an empty provider.
func New() *Provider {
	return &Provider{feedback: map[int][]hosting.Feedback{}}
}

func (p *Provider) record(c Call) { p.calls = append(p.calls, c) }

// RejectCredentials makes every later provider call fail as unauthorized.
func (p *Provider) RejectCredentials() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failure = func() error {
		return hosting.NewCredentialError("fake.token", "credentials rejected")
	}
}

// RateLimit makes every later provider call fail as rate limited until resetAt.
func (p *Provider) RateLimit(resetAt time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failure = func() error { return &hosting.RateLimitError{ResetAt: resetAt} }
}

// BringDown makes every later provider call fail as unavailable.
func (p *Provider) BringDown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failure = func() error { return fmt.Errorf("fake provider is down: %w", hosting.ErrUnavailable) }
}

// fail returns the configured failure, if any. The caller holds the lock.
func (p *Provider) fail() error {
	if p.failure == nil {
		return nil
	}
	return p.failure()
}

func (p *Provider) index(number int) int {
	if number < 1 || number > len(p.prs) {
		return -1
	}
	return number - 1
}

// CreatePullRequest opens a pull request numbered from 1.
func (p *Provider) CreatePullRequest(_ context.Context, n hosting.NewPullRequest) (hosting.PullRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.record(Call{Op: "create", Head: n.Head})
	if err := p.fail(); err != nil {
		return hosting.PullRequest{}, err
	}
	for _, pr := range p.prs {
		if pr.Head == n.Head && pr.State == hosting.StateOpen {
			return hosting.PullRequest{}, fmt.Errorf("pull request #%d is already open for %q: %w", pr.Number, n.Head, hosting.ErrConflict)
		}
	}
	number := len(p.prs) + 1
	pr := hosting.PullRequest{
		Number: number,
		URL:    fmt.Sprintf("https://fake.example/pull/%d", number),
		Title:  n.Title,
		Body:   n.Body,
		Head:   n.Head,
		Base:   n.Base,
		Draft:  n.Draft,
		State:  hosting.StateOpen,
	}
	p.prs = append(p.prs, pr)
	return pr, nil
}

// FindPullRequest prefers an open pull request over the newest closed or merged one.
func (p *Provider) FindPullRequest(_ context.Context, head string) (hosting.PullRequest, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.record(Call{Op: "find", Head: head})
	if err := p.fail(); err != nil {
		return hosting.PullRequest{}, false, err
	}
	var found hosting.PullRequest
	ok := false
	for _, pr := range p.prs {
		if pr.Head != head {
			continue
		}
		if pr.State == hosting.StateOpen {
			return pr, true, nil
		}
		found, ok = pr, true
	}
	return found, ok, nil
}

// GetPullRequest returns the pull request with the number.
func (p *Provider) GetPullRequest(_ context.Context, number int) (hosting.PullRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.record(Call{Op: "get", Number: number})
	if err := p.fail(); err != nil {
		return hosting.PullRequest{}, err
	}
	i := p.index(number)
	if i < 0 {
		return hosting.PullRequest{}, fmt.Errorf("pull request #%d: %w", number, hosting.ErrNotFound)
	}
	return p.prs[i], nil
}

// ReviewFeedback returns feedback after opts.Since, ordered by time then ID.
func (p *Provider) ReviewFeedback(_ context.Context, number int, opts hosting.FeedbackOptions) ([]hosting.Feedback, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.record(Call{Op: "feedback", Number: number})
	if err := p.fail(); err != nil {
		return nil, err
	}
	if p.index(number) < 0 {
		return nil, fmt.Errorf("pull request #%d: %w", number, hosting.ErrNotFound)
	}
	out := []hosting.Feedback{}
	for _, f := range p.feedback[number] {
		if !opts.Since.IsZero() && !f.CreatedAt.After(opts.Since) {
			continue
		}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Merge marks the pull request merged. It panics on an unknown number.
func (p *Provider) Merge(number int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pr := p.mustGet(number)
	pr.State = hosting.StateMerged
	pr.MergeCommit = fmt.Sprintf("%040x", number)
}

// Close marks the pull request closed without merging. It panics on an unknown number.
func (p *Provider) Close(number int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mustGet(number).State = hosting.StateClosed
}

// AddFeedback attaches feedback to the pull request. It panics on an unknown number.
func (p *Provider) AddFeedback(number int, f hosting.Feedback) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mustGet(number)
	p.feedback[number] = append(p.feedback[number], f)
}

// Calls returns the operations made so far, in order.
func (p *Provider) Calls() []Call {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Call(nil), p.calls...)
}

func (p *Provider) mustGet(number int) *hosting.PullRequest {
	i := p.index(number)
	if i < 0 {
		panic(fmt.Sprintf("fake: no pull request #%d", number))
	}
	return &p.prs[i]
}
