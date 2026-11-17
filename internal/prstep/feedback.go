package prstep

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/units"
)

const (
	feedbackReason      = "review feedback"
	feedbackTitleMax    = 100
	feedbackBodyMax     = 4000
	feedbackNoText      = "(no text; read the pull request for the reviewer's comments)"
	feedbackInstruction = "Make the change the reviewer asks for. If you decide not to, say why in your final answer."
	defaultReviewer     = "a reviewer"
)

// FeedbackOptions says what Feedback does with the feedback it reads.
type FeedbackOptions struct {
	// Create makes a task for each eligible item that has none.
	Create bool
	// All also makes general comments and plain reviews with text eligible.
	All bool
}

// FeedbackItem is one piece of feedback and what became of it.
type FeedbackItem struct {
	Feedback hosting.Feedback
	Eligible bool
	// Handled is the task that already exists for the item.
	Handled string
	// Created is the task made by this call.
	Created string
}

// FeedbackReport is what Feedback found and did.
type FeedbackReport struct {
	Slice       units.Unit
	Record      units.PullRequestRecord
	PullRequest hosting.PullRequest
	Items       []FeedbackItem
}

// FeedbackKey is the external link that marks a task as made from the item.
func FeedbackKey(number int, id string) string {
	return fmt.Sprintf("feedback:%d:%s", number, id)
}

// Feedback reads the review feedback on the latest pull request of a slice
// and, with Create, turns the eligible items that have no task yet into tasks
// appended to the slice. It changes nothing on the host.
func Feedback(ctx context.Context, d Deps, sliceID string, opt FeedbackOptions) (FeedbackReport, error) {
	var rep FeedbackReport
	slice, err := d.Units.Get(ctx, sliceID)
	if err != nil {
		return rep, err
	}
	if slice.Level != units.LevelSlice {
		return rep, fmt.Errorf("%s is a %s, not a slice: give ralph pr feedback the ID of a slice", slice.ID, slice.Level)
	}
	rep.Slice = slice
	records, err := d.Units.PullRequests(ctx, slice.ID)
	if err != nil {
		return rep, err
	}
	if len(records) == 0 {
		return rep, fmt.Errorf("%s has no pull request: run ralph pr create %s", slice.ID, slice.ID)
	}
	rep.Record = records[len(records)-1]
	if d.Provider == nil {
		return rep, errors.New("no pull request provider is configured")
	}
	p, err := d.Provider(ctx)
	if err != nil {
		return rep, err
	}
	number := rep.Record.Number
	pr, err := p.GetPullRequest(ctx, number)
	if errors.Is(err, hosting.ErrNotFound) {
		return rep, fmt.Errorf("pull request #%d of %s is gone from the host (%s): %w", number, slice.ID, rep.Record.URL, err)
	}
	if err != nil {
		return rep, err
	}
	rep.PullRequest = pr
	if opt.Create && pr.State != hosting.StateOpen {
		return rep, fmt.Errorf("pull request #%d of %s is %s: tasks are not created from its feedback", number, slice.ID, pr.State)
	}
	feedback, err := p.ReviewFeedback(ctx, number, hosting.FeedbackOptions{})
	if err != nil {
		return rep, err
	}
	for _, f := range feedback {
		item := FeedbackItem{Feedback: f, Eligible: eligible(f, opt.All)}
		linked, err := d.Units.UnitsLinkedTo(ctx, units.LinkExternal, FeedbackKey(number, f.ID))
		if err != nil {
			return rep, err
		}
		if len(linked) > 0 {
			item.Handled = linked[0].ID
		}
		rep.Items = append(rep.Items, item)
	}
	if !opt.Create {
		return rep, nil
	}
	for i := range rep.Items {
		item := &rep.Items[i]
		if !item.Eligible || item.Handled != "" {
			continue
		}
		id, err := createFeedbackTask(ctx, d.Units, slice.ID, pr, item.Feedback)
		item.Created = id
		if err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func eligible(f hosting.Feedback, all bool) bool {
	blank := strings.TrimSpace(f.Body) == ""
	switch f.Kind {
	case hosting.KindInline:
		return !blank
	case hosting.KindReview:
		switch f.State {
		case hosting.ReviewChangesRequested:
			return true
		case hosting.ReviewCommented:
			return all && !blank
		}
	case hosting.KindComment:
		return all && !blank
	}
	return false
}

// createFeedbackTask returns the ID of the task as soon as it exists, even
// when a later step fails.
func createFeedbackTask(ctx context.Context, svc units.Service, sliceID string, pr hosting.PullRequest, f hosting.Feedback) (string, error) {
	task, err := svc.Create(ctx, units.NewUnit{
		Level:              units.LevelTask,
		ParentID:           sliceID,
		Title:              feedbackTitle(f),
		Body:               feedbackBody(pr, f),
		AcceptanceCriteria: feedbackCriteria(f),
	})
	if err != nil {
		return "", fmt.Errorf("create a task for feedback %s: %w", f.ID, err)
	}
	fail := func(step string, err error) error {
		return fmt.Errorf("task %s was created for feedback %s but %s failed: %w", task.ID, f.ID, step, err)
	}
	if err := svc.AddLink(ctx, task.ID, units.LinkExternal, FeedbackKey(pr.Number, f.ID)); err != nil {
		return task.ID, fail("linking it", err)
	}
	if _, err := svc.SetState(ctx, task.ID, task.Fingerprint, units.StateTodo, feedbackReason); err != nil {
		return task.ID, fail("moving it to TODO", err)
	}
	url := f.URL
	if url == "" {
		url = pr.URL
	}
	if _, err := svc.AddNote(ctx, task.ID, units.NoteGeneral, "Created from review feedback "+url); err != nil {
		return task.ID, fail("adding its note", err)
	}
	return task.ID, nil
}

func reviewer(f hosting.Feedback) string {
	if strings.TrimSpace(f.Author) == "" {
		return defaultReviewer
	}
	return f.Author
}

func feedbackTitle(f hosting.Feedback) string {
	who := reviewer(f)
	var title string
	switch f.Kind {
	case hosting.KindInline:
		title = fmt.Sprintf("Address %s's comment on %s", who, location(f))
	case hosting.KindReview:
		title = "Address changes requested by " + who
	default:
		title = fmt.Sprintf("Respond to %s's comment", who)
	}
	return cut(title, feedbackTitleMax)
}

func location(f hosting.Feedback) string {
	if f.Line == 0 {
		return f.Path
	}
	return fmt.Sprintf("%s:%d", f.Path, f.Line)
}

// cut shortens s to at most max characters, the last being an ellipsis when
// anything was removed.
func cut(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func feedbackBody(pr hosting.PullRequest, f hosting.Feedback) string {
	who := reviewer(f)
	if who == defaultReviewer {
		who = "A reviewer"
	}
	text := strings.TrimSpace(strings.ReplaceAll(f.Body, "\r\n", "\n"))
	if text == "" {
		text = feedbackNoText
	}
	text = cut(text, feedbackBodyMax)
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s left this feedback on the pull request %s:\n\n%s\n\n", who, pr.URL, strings.Join(lines, "\n"))
	var refs []string
	if f.Kind == hosting.KindInline {
		refs = append(refs, "Location: "+location(f))
	}
	if f.URL != "" {
		refs = append(refs, "Feedback: "+f.URL)
	}
	if len(refs) > 0 {
		b.WriteString(strings.Join(refs, "\n") + "\n\n")
	}
	b.WriteString(feedbackInstruction)
	return b.String()
}

func feedbackCriteria(f hosting.Feedback) string {
	return fmt.Sprintf("- The change the reviewer asks for in %s is made, or the final answer says why it was not.\n- Every verification command passes.", f.ID)
}
