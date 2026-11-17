package prstep

import (
	"context"
	"fmt"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/units"
)

// ChangeKind says what Apply found.
type ChangeKind string

const (
	ChangeUnchanged      ChangeKind = "unchanged"
	ChangeClosed         ChangeKind = "closed"
	ChangeMerged         ChangeKind = "merged"
	ChangeReopenedOnHost ChangeKind = "reopened_on_host"
)

// Change is what Apply recorded; Message is the note it added, if any.
type Change struct {
	Kind    ChangeKind
	Message string
}

// Apply records the state of a pull request as the provider reported it. A
// merge is noted and nothing else changes.
func Apply(ctx context.Context, u units.Service, slice units.Unit, rec units.PullRequestRecord, pr hosting.PullRequest) (Change, error) {
	state := units.PullRequestState(pr.State)
	if state == rec.State {
		return Change{Kind: ChangeUnchanged}, nil
	}
	url := pr.URL
	if url == "" {
		url = rec.URL
	}
	var change Change
	switch {
	case state == units.PullRequestMerged:
		change = Change{Kind: ChangeMerged, Message: fmt.Sprintf("Pull request %s was merged.", url)}
	case state == units.PullRequestClosed && rec.State == units.PullRequestOpen:
		change = Change{Kind: ChangeClosed, Message: fmt.Sprintf("Pull request %s was closed without merging.", url)}
	case state == units.PullRequestOpen && rec.State == units.PullRequestClosed:
		change = Change{Kind: ChangeReopenedOnHost, Message: fmt.Sprintf("Pull request %s was reopened on the host.", url)}
	default:
		return Change{}, fmt.Errorf("pull request %s of %s went from %s to %s, which ralph does not handle", url, slice.ID, rec.State, state)
	}
	if err := u.SetPullRequestState(ctx, slice.ID, rec.Number, state); err != nil {
		return Change{}, err
	}
	if _, err := u.AddNote(ctx, slice.ID, units.NoteGeneral, change.Message); err != nil {
		return Change{}, err
	}
	return change, nil
}

// NeedsDecisionError reports a slice whose latest pull request was closed
// without merging and has no decision yet.
type NeedsDecisionError struct {
	Slice  string
	Number int
	URL    string
}

func (e *NeedsDecisionError) Error() string {
	return fmt.Sprintf("%s needs a decision: pull request #%d (%s) was closed without merging. Choose one: ralph pr reopen %s, ralph pr rearticulate %s or ralph pr cancel %s",
		e.Slice, e.Number, e.URL, e.Slice, e.Slice, e.Slice)
}

// Decision returns a NeedsDecisionError when the slice's latest pull request
// is closed with no resolution, else nil.
func Decision(ctx context.Context, u units.Service, sliceID string) error {
	records, err := u.PullRequests(ctx, sliceID)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	last := records[len(records)-1]
	if last.State == units.PullRequestClosed && last.Resolution == "" {
		return &NeedsDecisionError{Slice: sliceID, Number: last.Number, URL: last.URL}
	}
	return nil
}
