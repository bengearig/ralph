package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/hosting/github"
	"github.com/bengearig/ralph/internal/version"
)

const (
	defaultGitHubAPI = "https://api.github.com"
	githubHost       = "github.com"
)

// openProvider builds the pull request provider the configuration selects.
func openProvider(ctx context.Context, a *app) (hosting.Provider, error) {
	if a.root == "" {
		return nil, errors.New("not in a git repository")
	}
	if p := a.setting("pr.provider"); p != "github" {
		return nil, fmt.Errorf("unsupported pull request provider %q: set pr.provider to github", p)
	}
	apiURL := a.setting("github.api_url")
	owner, repo := a.setting("github.owner"), a.setting("github.repo")
	if owner == "" || repo == "" {
		remote := a.setting("pr.remote")
		r, err := hosting.DetectRemote(ctx, a.root, remote)
		if err != nil {
			return nil, fmt.Errorf("finding the GitHub repository: %w; set github.owner and github.repo to name it", err)
		}
		if r.Host != githubHost && apiURL == defaultGitHubAPI {
			return nil, fmt.Errorf("remote %q points at %s, which is not GitHub; set github.api_url to its API address", remote, r.Host)
		}
		owner, repo = r.Owner, r.Repo
	}
	timeout, err := time.ParseDuration(a.setting("pr.timeout"))
	if err != nil {
		return nil, fmt.Errorf("pr.timeout: %w", err)
	}
	return github.New(github.Config{
		APIURL:    apiURL,
		Owner:     owner,
		Repo:      repo,
		Token:     func() (string, error) { return a.cfg.Secret("github.token") },
		Timeout:   timeout,
		UserAgent: "ralph/" + version.String(),
	})
}

func (a *app) setting(path string) string {
	v, _ := a.cfg.Get(path)
	return v.Value
}
