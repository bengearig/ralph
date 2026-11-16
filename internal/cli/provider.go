package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/hosting/bitbucket"
	"github.com/bengearig/ralph/internal/hosting/github"
	"github.com/bengearig/ralph/internal/version"
)

const (
	defaultGitHubAPI = "https://api.github.com"
	githubHost       = "github.com"
)

type providerOpener func(ctx context.Context, a *app) (hosting.Provider, error)

var providerOpeners = map[string]providerOpener{
	"github":    openGitHub,
	"bitbucket": openBitbucket,
}

// openProvider builds the pull request provider the configuration selects.
func openProvider(ctx context.Context, a *app) (hosting.Provider, error) {
	if a.root == "" {
		return nil, errors.New("not in a git repository")
	}
	name, err := resolveProvider(ctx, a)
	if err != nil {
		return nil, err
	}
	open, ok := providerOpeners[name]
	if !ok {
		return nil, fmt.Errorf("pull request provider %q is not supported by this build", name)
	}
	return open(ctx, a)
}

// resolveProvider turns pr.provider into a provider name, choosing from the
// remote when it is auto.
func resolveProvider(ctx context.Context, a *app) (string, error) {
	if p := a.setting("pr.provider"); p != "auto" {
		return p, nil
	}
	remote := a.setting("pr.remote")
	r, remoteErr := hosting.DetectRemote(ctx, a.root, remote)
	if remoteErr == nil {
		if name := hosting.ProviderForHost(r.Host); name != "" {
			return name, nil
		}
	}
	githubPair := a.setting("github.owner") != "" && a.setting("github.repo") != ""
	bitbucketPair := a.setting("bitbucket.workspace") != "" && a.setting("bitbucket.repo") != ""
	switch {
	case githubPair && bitbucketPair:
		return "", errors.New("github.owner and github.repo and bitbucket.workspace and bitbucket.repo are all set; set pr.provider to github or bitbucket")
	case githubPair:
		return "github", nil
	case bitbucketPair:
		return "bitbucket", nil
	}
	if a.setting("github.api_url") != defaultGitHubAPI {
		return "github", nil
	}
	which := ""
	if remoteErr == nil {
		which = fmt.Sprintf(" remote %q (%s) uses", remote, r.Host)
	}
	return "", fmt.Errorf("cannot tell which pull request provider%s; set pr.provider to github or bitbucket, and for GitHub Enterprise set github.api_url", which)
}

func openGitHub(ctx context.Context, a *app) (hosting.Provider, error) {
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

func openBitbucket(ctx context.Context, a *app) (hosting.Provider, error) {
	workspace, repo := a.setting("bitbucket.workspace"), a.setting("bitbucket.repo")
	if workspace == "" || repo == "" {
		remote := a.setting("pr.remote")
		r, err := hosting.DetectRemote(ctx, a.root, remote)
		if err != nil {
			return nil, fmt.Errorf("finding the Bitbucket repository: %w; set bitbucket.workspace and bitbucket.repo to name it", err)
		}
		if hosting.ProviderForHost(r.Host) != "bitbucket" {
			return nil, fmt.Errorf("remote %q points at %s, which is not Bitbucket Cloud; set bitbucket.workspace and bitbucket.repo", remote, r.Host)
		}
		workspace, repo = r.Owner, r.Repo
	}
	timeout, err := time.ParseDuration(a.setting("pr.timeout"))
	if err != nil {
		return nil, fmt.Errorf("pr.timeout: %w", err)
	}
	return bitbucket.New(bitbucket.Config{
		APIURL:    a.setting("bitbucket.api_url"),
		Workspace: workspace,
		Repo:      repo,
		User:      a.setting("bitbucket.user"),
		Token:     func() (string, error) { return a.cfg.Secret("bitbucket.token") },
		Timeout:   timeout,
		UserAgent: "ralph/" + version.String(),
	})
}

func (a *app) setting(path string) string {
	v, _ := a.cfg.Get(path)
	return v.Value
}
