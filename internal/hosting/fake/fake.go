// Package fake is an in-memory hosting.Provider for tests.
package fake

import (
	"context"
	"fmt"
	"sort"
	"sync"

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
}

// New returns an empty provider.
func New() *Provider {
	return &Provider{feedback: map[int][]hosting.Feedback{}}
}

func (p *Provider) record(c Call) { p.calls = append(p.calls, c) }

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
	i := p.index(number)
	if i < 0 {
		return hosting.PullRequest{}, fmt.Errorf("pull request #%d: %w", number, hosting.ErrNotFound)
	}
	return p.prs[i], nil
}

// ReviewFeedback returns feedback at or after opts.Since, ordered by time then ID.
func (p *Provider) ReviewFeedback(_ context.Context, number int, opts hosting.FeedbackOptions) ([]hosting.Feedback, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.record(Call{Op: "feedback", Number: number})
	if p.index(number) < 0 {
		return nil, fmt.Errorf("pull request #%d: %w", number, hosting.ErrNotFound)
	}
	var out []hosting.Feedback
	for _, f := range p.feedback[number] {
		if !opts.Since.IsZero() && f.CreatedAt.Before(opts.Since) {
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
