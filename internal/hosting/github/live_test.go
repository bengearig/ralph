package github

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

// TestLiveGitHub reads from real GitHub and never creates or changes anything.
func TestLiveGitHub(t *testing.T) {
	if os.Getenv("RALPH_GITHUB_LIVE") != "1" {
		t.Skip("set RALPH_GITHUB_LIVE=1 to run against real GitHub")
	}
	repo := os.Getenv("RALPH_GITHUB_LIVE_REPO")
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		t.Fatalf("RALPH_GITHUB_LIVE_REPO must be owner/repo, got %q", repo)
	}
	branch := os.Getenv("RALPH_GITHUB_LIVE_BRANCH")
	if branch == "" {
		t.Fatal("RALPH_GITHUB_LIVE_BRANCH must name the branch to look up")
	}
	tokenEnv := os.Getenv("RALPH_GITHUB_LIVE_TOKEN_ENV")
	if tokenEnv == "" {
		tokenEnv = "GITHUB_TOKEN"
	}
	token := os.Getenv(tokenEnv)
	if token == "" {
		t.Fatalf("the token variable %s is empty", tokenEnv)
	}

	p, err := New(Config{Owner: owner, Repo: name, Token: func() (string, error) { return token, nil }})
	if err != nil {
		t.Fatal(err)
	}
	var provider hosting.Provider = p
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	pr, found, err := provider.FindPullRequest(ctx, branch)
	if errors.Is(err, hosting.ErrNotFound) {
		t.Logf("repository %s is not visible to this token: %v", repo, err)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Logf("no pull request for branch %q", branch)
		return
	}
	got, err := provider.GetPullRequest(ctx, pr.Number)
	if err != nil && !errors.Is(err, hosting.ErrNotFound) {
		t.Fatal(err)
	}
	feedback, err := provider.ReviewFeedback(ctx, pr.Number, hosting.FeedbackOptions{})
	if err != nil && !errors.Is(err, hosting.ErrNotFound) {
		t.Fatal(err)
	}
	t.Logf("pull request #%d is %s with %d feedback items", pr.Number, got.State, len(feedback))
}
