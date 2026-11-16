package bitbucket

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

// TestLiveBitbucket reads from real Bitbucket Cloud and never creates or changes anything.
func TestLiveBitbucket(t *testing.T) {
	if os.Getenv("RALPH_BITBUCKET_LIVE") != "1" {
		t.Skip("set RALPH_BITBUCKET_LIVE=1 to run against real Bitbucket Cloud")
	}
	repo := os.Getenv("RALPH_BITBUCKET_LIVE_REPO")
	workspace, name, ok := strings.Cut(repo, "/")
	if !ok || workspace == "" || name == "" {
		t.Fatalf("RALPH_BITBUCKET_LIVE_REPO must be workspace/repo, got %q", repo)
	}
	branch := os.Getenv("RALPH_BITBUCKET_LIVE_BRANCH")
	if branch == "" {
		branch = "main"
	}
	tokenEnv := os.Getenv("RALPH_BITBUCKET_LIVE_TOKEN_ENV")
	if tokenEnv == "" {
		tokenEnv = "BITBUCKET_TOKEN"
	}
	token := os.Getenv(tokenEnv)
	if token == "" {
		t.Fatalf("the token variable %s is empty", tokenEnv)
	}

	p, err := New(Config{
		Workspace: workspace, Repo: name, User: os.Getenv("RALPH_BITBUCKET_LIVE_USER"),
		Token: func() (string, error) { return token, nil },
	})
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
