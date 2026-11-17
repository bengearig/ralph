// Package issuetest holds an in-memory issuesource.Source and the conformance
// suite every source must pass.
package issuetest

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/bengearig/ralph/internal/issuesource"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-[0-9]+$`)

// Fake is an in-memory issuesource.Source.
type Fake struct {
	mu     sync.Mutex
	system string
	host   string
	issues map[string]issuesource.Issue
	calls  []string
	err    error
}

var _ issuesource.Source = (*Fake)(nil)

// NewFake returns a fake named system, which accepts addresses on host. An
// empty system is jira.
func NewFake(system, host string) *Fake {
	if system == "" {
		system = "jira"
	}
	return &Fake{system: system, host: host, issues: map[string]issuesource.Issue{}}
}

// Name returns the system name.
func (f *Fake) Name() string { return f.system }

// Add stores i under its key, upper-cased, and fills in what it leaves empty.
func (f *Fake) Add(i issuesource.Issue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i.Ref.System = f.system
	i.Ref.Key = strings.ToUpper(i.Ref.Key)
	if i.URL == "" {
		i.URL = fmt.Sprintf("https://%s/browse/%s", f.host, i.Ref.Key)
	}
	f.issues[i.Ref.Key] = i
}

// Remove forgets the ticket with key.
func (f *Fake) Remove(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.issues, strings.ToUpper(key))
}

// FailWith makes Get return err until it is called with nil.
func (f *Fake) FailWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Calls returns the Get calls made, each as the ref's String.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *Fake) accepted() string {
	return fmt.Sprintf("use a key such as ABC-123 or a URL such as https://%s/browse/ABC-123", f.host)
}

// Parse accepts a key and an address https://<host>/browse/<KEY>.
func (f *Fake) Parse(ref string) (issuesource.Ref, error) {
	ref = strings.TrimSpace(ref)
	if keyPattern.MatchString(ref) {
		return issuesource.Ref{System: f.system, Key: strings.ToUpper(ref)}, nil
	}
	u, err := url.Parse(ref)
	if err == nil && u.Scheme == "https" && strings.EqualFold(u.Host, f.host) {
		if key, ok := strings.CutPrefix(u.Path, "/browse/"); ok && keyPattern.MatchString(key) {
			return issuesource.Ref{System: f.system, Key: strings.ToUpper(key)}, nil
		}
	}
	return issuesource.Ref{}, fmt.Errorf("%w: not a %s ticket: %s", issuesource.ErrInvalid, f.system, f.accepted())
}

// Get returns the ticket, ErrNotFound when it is not there.
func (f *Fake) Get(_ context.Context, ref issuesource.Ref) (issuesource.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ref.String())
	if f.err != nil {
		return issuesource.Issue{}, f.err
	}
	if ref.System != f.system {
		return issuesource.Issue{}, fmt.Errorf("%w: %s is not a %s ticket", issuesource.ErrInvalid, ref, f.system)
	}
	i, ok := f.issues[ref.Key]
	if !ok {
		return issuesource.Issue{}, fmt.Errorf("%w: %s was not found", issuesource.ErrNotFound, ref.Key)
	}
	return i, nil
}
