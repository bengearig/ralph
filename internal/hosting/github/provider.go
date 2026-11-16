package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

type pullRequestJSON struct {
	Number         int        `json:"number"`
	HTMLURL        string     `json:"html_url"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	State          string     `json:"state"`
	Draft          bool       `json:"draft"`
	MergedAt       *time.Time `json:"merged_at"`
	MergeCommitSHA string     `json:"merge_commit_sha"`
	Head           struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (j pullRequestJSON) toPullRequest() hosting.PullRequest {
	pr := hosting.PullRequest{
		Number: j.Number,
		URL:    j.HTMLURL,
		Title:  j.Title,
		Body:   j.Body,
		Head:   j.Head.Ref,
		Base:   j.Base.Ref,
		Draft:  j.Draft,
		State:  hosting.StateClosed,
	}
	switch {
	case j.State == "open":
		pr.State = hosting.StateOpen
	case j.MergedAt != nil:
		pr.State = hosting.StateMerged
		pr.MergeCommit = j.MergeCommitSHA
	}
	return pr
}

// CreatePullRequest opens a pull request from a branch of the repository.
func (p *Provider) CreatePullRequest(ctx context.Context, pr hosting.NewPullRequest) (hosting.PullRequest, error) {
	in := struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
		Draft bool   `json:"draft"`
	}{pr.Title, pr.Head, pr.Base, pr.Body, pr.Draft}
	var out pullRequestJSON
	if err := p.do(ctx, http.MethodPost, p.repoPath("/pulls"), nil, in, &out); err != nil {
		return hosting.PullRequest{}, err
	}
	return out.toPullRequest(), nil
}

// FindPullRequest returns the newest open pull request for head, else the newest of any state.
func (p *Provider) FindPullRequest(ctx context.Context, head string) (hosting.PullRequest, bool, error) {
	q := url.Values{
		"head":      {p.owner + ":" + head},
		"state":     {"all"},
		"sort":      {"created"},
		"direction": {"desc"},
		"per_page":  {"100"},
	}
	var list []pullRequestJSON
	if err := p.do(ctx, http.MethodGet, p.repoPath("/pulls"), q, nil, &list); err != nil {
		return hosting.PullRequest{}, false, err
	}
	if len(list) == 0 {
		return hosting.PullRequest{}, false, nil
	}
	for _, j := range list {
		if j.State == "open" {
			return j.toPullRequest(), true, nil
		}
	}
	return list[0].toPullRequest(), true, nil
}

// GetPullRequest is not implemented yet.
func (p *Provider) GetPullRequest(context.Context, int) (hosting.PullRequest, error) {
	return hosting.PullRequest{}, fmt.Errorf("github: reading a pull request: %w", errUnsupported)
}

// ReviewFeedback is not implemented yet.
func (p *Provider) ReviewFeedback(context.Context, int, hosting.FeedbackOptions) ([]hosting.Feedback, error) {
	return nil, fmt.Errorf("github: reading review feedback: %w", errUnsupported)
}
