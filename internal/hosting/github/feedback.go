package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

const (
	feedbackPerPage = "100"
	maxFeedbackPage = 50
)

// GetPullRequest returns the pull request with the given number, or ErrNotFound.
func (p *Provider) GetPullRequest(ctx context.Context, number int) (hosting.PullRequest, error) {
	var out pullRequestJSON
	if err := p.do(ctx, http.MethodGet, p.repoPath("/pulls/"+strconv.Itoa(number)), nil, nil, &out); err != nil {
		return hosting.PullRequest{}, err
	}
	return out.toPullRequest(), nil
}

type feedbackUser struct {
	Login string `json:"login"`
}

type reviewJSON struct {
	ID          int64        `json:"id"`
	User        feedbackUser `json:"user"`
	Body        string       `json:"body"`
	State       string       `json:"state"`
	HTMLURL     string       `json:"html_url"`
	SubmittedAt time.Time    `json:"submitted_at"`
}

type commentJSON struct {
	ID           int64        `json:"id"`
	User         feedbackUser `json:"user"`
	Body         string       `json:"body"`
	HTMLURL      string       `json:"html_url"`
	CreatedAt    time.Time    `json:"created_at"`
	Path         string       `json:"path"`
	Line         *int         `json:"line"`
	OriginalLine *int         `json:"original_line"`
}

// ReviewFeedback combines reviews, inline comments and conversation comments, oldest first.
func (p *Provider) ReviewFeedback(ctx context.Context, number int, opts hosting.FeedbackOptions) ([]hosting.Feedback, error) {
	n := strconv.Itoa(number)
	feedback := []hosting.Feedback{}

	reviews, err := fetchAll[reviewJSON](ctx, p, p.repoPath("/pulls/"+n+"/reviews"))
	if err != nil {
		return nil, err
	}
	for _, r := range reviews {
		var state hosting.ReviewState
		switch strings.ToUpper(r.State) {
		case "CHANGES_REQUESTED":
			state = hosting.ReviewChangesRequested
		case "APPROVED":
			state = hosting.ReviewApproved
		case "COMMENTED":
			if strings.TrimSpace(r.Body) == "" {
				continue
			}
			state = hosting.ReviewCommented
		default:
			continue
		}
		feedback = append(feedback, hosting.Feedback{
			ID:        "review-" + strconv.FormatInt(r.ID, 10),
			Kind:      hosting.KindReview,
			Author:    r.User.Login,
			Body:      r.Body,
			State:     state,
			CreatedAt: r.SubmittedAt,
			URL:       r.HTMLURL,
		})
	}

	inline, err := fetchAll[commentJSON](ctx, p, p.repoPath("/pulls/"+n+"/comments"))
	if err != nil {
		return nil, err
	}
	for _, c := range inline {
		line := 0
		switch {
		case c.Line != nil:
			line = *c.Line
		case c.OriginalLine != nil:
			line = *c.OriginalLine
		}
		feedback = append(feedback, hosting.Feedback{
			ID:        "inline-" + strconv.FormatInt(c.ID, 10),
			Kind:      hosting.KindInline,
			Author:    c.User.Login,
			Body:      c.Body,
			Path:      c.Path,
			Line:      line,
			CreatedAt: c.CreatedAt,
			URL:       c.HTMLURL,
		})
	}

	comments, err := fetchAll[commentJSON](ctx, p, p.repoPath("/issues/"+n+"/comments"))
	if err != nil {
		return nil, err
	}
	for _, c := range comments {
		feedback = append(feedback, hosting.Feedback{
			ID:        "comment-" + strconv.FormatInt(c.ID, 10),
			Kind:      hosting.KindComment,
			Author:    c.User.Login,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
			URL:       c.HTMLURL,
		})
	}

	if !opts.Since.IsZero() {
		kept := feedback[:0]
		for _, f := range feedback {
			if f.CreatedAt.After(opts.Since) {
				kept = append(kept, f)
			}
		}
		feedback = kept
	}
	sort.SliceStable(feedback, func(i, j int) bool {
		if !feedback[i].CreatedAt.Equal(feedback[j].CreatedAt) {
			return feedback[i].CreatedAt.Before(feedback[j].CreatedAt)
		}
		return feedback[i].ID < feedback[j].ID
	})
	return feedback, nil
}

// fetchAll reads every page of a list endpoint by following the Link header's next relation.
func fetchAll[T any](ctx context.Context, p *Provider, path string) ([]T, error) {
	query := url.Values{"per_page": {feedbackPerPage}}
	var all []T
	for page := 1; ; page++ {
		var items []T
		header, err := p.send(ctx, http.MethodGet, path, query, nil, &items)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		next, ok := nextPageQuery(header.Values("Link"))
		if !ok {
			return all, nil
		}
		if page >= maxFeedbackPage {
			return nil, fmt.Errorf("github: the feedback is too large to read (more than %d pages of %s)", maxFeedbackPage, path)
		}
		query = next
	}
}

// nextPageQuery returns the query of the rel="next" link. Only the query is used, so credentials never go to another host.
func nextPageQuery(links []string) (url.Values, bool) {
	for _, header := range links {
		for _, part := range strings.Split(header, ",") {
			segs := strings.Split(part, ";")
			if len(segs) < 2 {
				continue
			}
			isNext := false
			for _, param := range segs[1:] {
				param = strings.ReplaceAll(strings.TrimSpace(param), " ", "")
				if param == `rel="next"` || param == "rel=next" {
					isNext = true
				}
			}
			target := strings.TrimSpace(segs[0])
			if !isNext || !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
				continue
			}
			u, err := url.Parse(target[1 : len(target)-1])
			if err != nil {
				return nil, false
			}
			q := u.Query()
			q.Set("per_page", feedbackPerPage)
			return q, true
		}
	}
	return nil, false
}
