package bitbucket

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bengearig/ralph/internal/hosting"
)

type branchRef struct {
	Branch struct {
		Name string `json:"name"`
	} `json:"branch"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

type pullRequestJSON struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	State       string    `json:"state"`
	Draft       bool      `json:"draft"`
	Source      branchRef `json:"source"`
	Destination branchRef `json:"destination"`
	MergeCommit *struct {
		Hash string `json:"hash"`
	} `json:"merge_commit"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

func (j pullRequestJSON) toPullRequest() hosting.PullRequest {
	pr := hosting.PullRequest{
		Number: j.ID,
		URL:    j.Links.HTML.Href,
		Title:  j.Title,
		Body:   j.Description,
		Head:   j.Source.Branch.Name,
		Base:   j.Destination.Branch.Name,
		Draft:  j.Draft,
		State:  hosting.StateClosed,
	}
	switch j.State {
	case "OPEN":
		pr.State = hosting.StateOpen
	case "MERGED":
		pr.State = hosting.StateMerged
		if j.MergeCommit != nil {
			pr.MergeCommit = j.MergeCommit.Hash
		}
	}
	return pr
}

type branchName struct {
	Name string `json:"name"`
}

type branchBody struct {
	Branch branchName `json:"branch"`
}

// CreatePullRequest opens a pull request from a branch of the repository.
func (p *Provider) CreatePullRequest(ctx context.Context, pr hosting.NewPullRequest) (hosting.PullRequest, error) {
	in := struct {
		Title             string      `json:"title"`
		Description       string      `json:"description"`
		Source            branchBody  `json:"source"`
		Destination       *branchBody `json:"destination,omitempty"`
		Draft             bool        `json:"draft"`
		CloseSourceBranch bool        `json:"close_source_branch"`
	}{
		Title:       pr.Title,
		Description: pr.Body,
		Source:      branchBody{branchName{pr.Head}},
		Draft:       pr.Draft,
	}
	if pr.Base != "" {
		in.Destination = &branchBody{branchName{pr.Base}}
	}
	var out pullRequestJSON
	if err := p.do(ctx, http.MethodPost, p.repoPath("/pullrequests"), nil, in, &out); err != nil {
		return hosting.PullRequest{}, err
	}
	return out.toPullRequest(), nil
}

// FindPullRequest returns the open pull request for head, else the newest of any state.
func (p *Provider) FindPullRequest(ctx context.Context, head string) (hosting.PullRequest, bool, error) {
	filter := `source.branch.name="` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(head) + `"`
	attempts := []url.Values{
		{"q": {filter}, "state": {"OPEN"}, "pagelen": {"50"}},
		{"q": {filter}, "state": {"OPEN", "MERGED", "DECLINED", "SUPERSEDED"}, "sort": {"-created_on"}, "pagelen": {"50"}},
	}
	for _, q := range attempts {
		var pg page[pullRequestJSON]
		if err := p.do(ctx, http.MethodGet, p.repoPath("/pullrequests"), q, nil, &pg); err != nil {
			return hosting.PullRequest{}, false, err
		}
		for _, j := range pg.Values {
			if p.ownsSource(j) {
				return j.toPullRequest(), true, nil
			}
		}
	}
	return hosting.PullRequest{}, false, nil
}

// ownsSource is false for a pull request whose source branch lives in another repository.
func (p *Provider) ownsSource(j pullRequestJSON) bool {
	full := j.Source.Repository.FullName
	return full == "" || strings.EqualFold(full, p.workspace+"/"+p.repo)
}

// GetPullRequest returns the pull request with the given number.
func (p *Provider) GetPullRequest(ctx context.Context, number int) (hosting.PullRequest, error) {
	var out pullRequestJSON
	if err := p.do(ctx, http.MethodGet, p.repoPath("/pullrequests/"+strconv.Itoa(number)), nil, nil, &out); err != nil {
		return hosting.PullRequest{}, err
	}
	return out.toPullRequest(), nil
}

// ReviewFeedback is not implemented yet.
func (p *Provider) ReviewFeedback(context.Context, int, hosting.FeedbackOptions) ([]hosting.Feedback, error) {
	return nil, errors.New("bitbucket: reading review feedback is not implemented yet")
}
