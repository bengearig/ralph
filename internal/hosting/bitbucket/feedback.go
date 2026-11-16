package bitbucket

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

type feedbackUser struct {
	DisplayName string `json:"display_name"`
	Nickname    string `json:"nickname"`
	AccountID   string `json:"account_id"`
	UUID        string `json:"uuid"`
}

func (u feedbackUser) name() string {
	switch {
	case u.DisplayName != "":
		return u.DisplayName
	case u.Nickname != "":
		return u.Nickname
	}
	return "unknown"
}

type htmlLinks struct {
	HTML struct {
		Href string `json:"href"`
	} `json:"html"`
}

type commentJSON struct {
	ID      int          `json:"id"`
	User    feedbackUser `json:"user"`
	Created string       `json:"created_on"`
	Deleted bool         `json:"deleted"`
	Pending bool         `json:"pending"`
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
	Inline *struct {
		Path string `json:"path"`
		From *int   `json:"from"`
		To   *int   `json:"to"`
	} `json:"inline"`
	Links htmlLinks `json:"links"`
}

type participantJSON struct {
	User           feedbackUser `json:"user"`
	State          string       `json:"state"`
	Approved       bool         `json:"approved"`
	ParticipatedOn string       `json:"participated_on"`
}

type participantsJSON struct {
	Participants []participantJSON `json:"participants"`
	Links        htmlLinks         `json:"links"`
}

// ReviewFeedback combines comments and reviewer verdicts, oldest first.
//
// Bitbucket keeps only each reviewer's current verdict, so an approval or a request for changes that a later
// verdict replaced is not seen. Resolved comments are included, as are replies.
func (p *Provider) ReviewFeedback(ctx context.Context, number int, opts hosting.FeedbackOptions) ([]hosting.Feedback, error) {
	n := strconv.Itoa(number)
	feedback := []hosting.Feedback{}

	query := url.Values{"pagelen": {"100"}, "sort": {"created_on"}}
	comments, err := collect[commentJSON](ctx, p, p.repoPath("/pullrequests/"+n+"/comments"), query)
	if err != nil {
		return nil, err
	}
	for _, c := range comments {
		if c.Deleted || c.Pending {
			continue
		}
		id := "comment-" + strconv.Itoa(c.ID)
		created, err := parseTime(c.Created, "created_on", id)
		if err != nil {
			return nil, err
		}
		f := hosting.Feedback{
			ID:        id,
			Kind:      hosting.KindComment,
			Author:    c.User.name(),
			Body:      c.Content.Raw,
			CreatedAt: created,
			URL:       c.Links.HTML.Href,
		}
		if c.Inline != nil {
			f.Kind = hosting.KindInline
			f.Path = c.Inline.Path
			switch {
			case c.Inline.To != nil:
				f.Line = *c.Inline.To
			case c.Inline.From != nil:
				f.Line = *c.Inline.From
			}
		}
		feedback = append(feedback, f)
	}

	var pr participantsJSON
	if err := p.do(ctx, http.MethodGet, p.repoPath("/pullrequests/"+n), nil, nil, &pr); err != nil {
		return nil, err
	}
	for _, part := range pr.Participants {
		var state hosting.ReviewState
		switch strings.ToLower(part.State) {
		case "changes_requested":
			state = hosting.ReviewChangesRequested
		case "approved":
			state = hosting.ReviewApproved
		case "":
			if !part.Approved {
				continue
			}
			state = hosting.ReviewApproved
		default:
			continue
		}
		author := part.User.name()
		key := part.User.AccountID
		if key == "" {
			key = part.User.UUID
		}
		if key == "" {
			key = author
		}
		id := "review-" + key
		at, err := parseTime(part.ParticipatedOn, "participated_on", id)
		if err != nil {
			return nil, err
		}
		feedback = append(feedback, hosting.Feedback{
			ID:        id,
			Kind:      hosting.KindReview,
			Author:    author,
			State:     state,
			CreatedAt: at,
			URL:       pr.Links.HTML.Href,
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

// parseTime reads an RFC 3339 time, with or without fractional seconds; an absent value is the zero time.
func parseTime(value, field, id string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("bitbucket: %s of %s is not a valid time: %q", field, id, value)
	}
	return t, nil
}
