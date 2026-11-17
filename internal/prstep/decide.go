package prstep

import (
	"context"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/units"
)

// NotWaitingError reports a slice whose latest pull request is not closed
// without a decision.
type NotWaitingError struct{ Slice string }

func (e *NotWaitingError) Error() string {
	return fmt.Sprintf("%s is not waiting for a decision", e.Slice)
}

// waitingRecord returns the slice and its latest pull request when that one
// was closed without merging and has no decision yet.
func waitingRecord(ctx context.Context, u units.Service, sliceID string) (units.Unit, units.PullRequestRecord, error) {
	slice, err := u.Get(ctx, sliceID)
	if err != nil {
		return units.Unit{}, units.PullRequestRecord{}, err
	}
	if slice.Level != units.LevelSlice {
		return units.Unit{}, units.PullRequestRecord{}, fmt.Errorf("%s is a %s, not a slice: give the ID of a slice", slice.ID, slice.Level)
	}
	records, err := u.PullRequests(ctx, slice.ID)
	if err != nil {
		return units.Unit{}, units.PullRequestRecord{}, err
	}
	if len(records) == 0 {
		return units.Unit{}, units.PullRequestRecord{}, &NotWaitingError{Slice: slice.ID}
	}
	last := records[len(records)-1]
	if last.State != units.PullRequestClosed || last.Resolution != "" {
		return units.Unit{}, units.PullRequestRecord{}, &NotWaitingError{Slice: slice.ID}
	}
	return slice, last, nil
}

// ReopenResult is what Reopen did.
type ReopenResult struct {
	Created CreateResult
	// Resolved is the old pull request, now resolved as reopened.
	Resolved units.PullRequestRecord
}

// Reopen opens a new pull request from the slice's branch and resolves the
// closed one as reopened. Nothing is resolved when opening fails.
func Reopen(ctx context.Context, d Deps, sliceID string) (ReopenResult, error) {
	slice, old, err := waitingRecord(ctx, d.Units, sliceID)
	if err != nil {
		return ReopenResult{}, err
	}
	created, err := Create(ctx, d, CreateInput{SliceID: slice.ID, skipDecision: true})
	if err != nil {
		return ReopenResult{}, err
	}
	if created.PullRequest.Number == old.Number {
		return ReopenResult{}, fmt.Errorf("pull request #%d of %s is open again on the host, so nothing is left to decide", old.Number, slice.ID)
	}
	if err := d.Units.ResolvePullRequest(ctx, slice.ID, old.Number, "reopened"); err != nil {
		return ReopenResult{}, fmt.Errorf("the new pull request is open at %s but resolving #%d failed: %w", created.PullRequest.URL, old.Number, err)
	}
	note := fmt.Sprintf("Pull request %s was closed without merging; reopened as %s.", old.URL, created.PullRequest.URL)
	if _, err := d.Units.AddNote(ctx, slice.ID, units.NoteGeneral, note); err != nil {
		return ReopenResult{}, err
	}
	old.Resolution = "reopened"
	return ReopenResult{Created: created, Resolved: old}, nil
}

// RearticulateResult is what Rearticulate did.
type RearticulateResult struct {
	// Moved are the tasks now PROPOSED; Kept are the DONE tasks.
	Moved, Kept []string
	Resolved    units.PullRequestRecord
}

// sliceTasks returns the tasks of a slice in position order.
func sliceTasks(ctx context.Context, u units.Service, sliceID string) ([]units.Unit, error) {
	return u.List(ctx, units.Filter{Level: units.LevelTask, ParentID: &sliceID})
}

func unfinished(t units.Unit) bool {
	return t.State != units.StateDone && t.State != units.StateCancelled
}

// Rearticulate moves the unfinished tasks of the slice back to PROPOSED and
// resolves the closed pull request as rearticulated.
func Rearticulate(ctx context.Context, u units.Service, sliceID string) (RearticulateResult, error) {
	slice, rec, err := waitingRecord(ctx, u, sliceID)
	if err != nil {
		return RearticulateResult{}, err
	}
	tasks, err := sliceTasks(ctx, u, slice.ID)
	if err != nil {
		return RearticulateResult{}, err
	}
	res := RearticulateResult{Moved: []string{}, Kept: []string{}}
	reason := fmt.Sprintf("pull request %s was closed without merging; plan this slice again", rec.URL)
	for _, t := range tasks {
		switch {
		case t.State == units.StateDone:
			res.Kept = append(res.Kept, t.ID)
			continue
		case !unfinished(t):
			continue
		}
		cur := t
		if cur.State == units.StateInProgress {
			if cur, err = u.SetState(ctx, cur.ID, cur.Fingerprint, units.StateTodo, "loop interrupted by a closed pull request"); err != nil {
				return res, err
			}
		}
		if cur.State == units.StateProposed {
			_, err = u.AddNote(ctx, cur.ID, units.NoteKickback, reason)
		} else {
			_, err = u.SetState(ctx, cur.ID, cur.Fingerprint, units.StateProposed, reason)
		}
		if err != nil {
			return res, err
		}
		res.Moved = append(res.Moved, cur.ID)
	}
	if err := u.ResolvePullRequest(ctx, slice.ID, rec.Number, "rearticulated"); err != nil {
		return res, err
	}
	note := fmt.Sprintf("Pull request %s was closed without merging; plan this slice again.", rec.URL)
	if len(res.Moved) > 0 {
		note += " Moved back to PROPOSED: " + strings.Join(res.Moved, ", ") + "."
	}
	if _, err := u.AddNote(ctx, slice.ID, units.NoteGeneral, note); err != nil {
		return res, err
	}
	rec.Resolution = "rearticulated"
	res.Resolved = rec
	return res, nil
}

// CancelOptions says how far Cancel goes.
type CancelOptions struct {
	// Dependents also cancels the unfinished tasks of the slices that wait
	// for this one.
	Dependents bool
	// Yes does it; without it nothing changes and the result says what would.
	Yes bool
}

// CancelResult says what Cancel cancelled, or with Done false what it would.
type CancelResult struct {
	Tasks []string
	// Dependents are the slices waiting for the slice; DependentTasks are
	// their unfinished tasks, filled only when CancelOptions.Dependents is set.
	Dependents     []string
	DependentTasks []string
	Done           bool
	Resolved       units.PullRequestRecord
}

func unfinishedTasks(ctx context.Context, u units.Service, sliceID string) ([]units.Unit, error) {
	tasks, err := sliceTasks(ctx, u, sliceID)
	if err != nil {
		return nil, err
	}
	var out []units.Unit
	for _, t := range tasks {
		if unfinished(t) {
			out = append(out, t)
		}
	}
	return out, nil
}

func cancelTasks(ctx context.Context, u units.Service, tasks []units.Unit, reason string) error {
	for _, t := range tasks {
		if _, err := u.SetState(ctx, t.ID, t.Fingerprint, units.StateCancelled, reason); err != nil {
			return err
		}
	}
	return nil
}

func taskIDs(tasks []units.Unit) []string {
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	return ids
}

// Cancel cancels the unfinished tasks of the slice, and with the Dependents
// option those of the slices waiting for it, and resolves the closed pull
// request as cancelled. DONE tasks are never touched.
func Cancel(ctx context.Context, u units.Service, sliceID string, opt CancelOptions) (CancelResult, error) {
	slice, rec, err := waitingRecord(ctx, u, sliceID)
	if err != nil {
		return CancelResult{}, err
	}
	own, err := unfinishedTasks(ctx, u, slice.ID)
	if err != nil {
		return CancelResult{}, err
	}
	res := CancelResult{Tasks: taskIDs(own), DependentTasks: []string{}}
	if res.Dependents, err = u.Dependents(ctx, slice.ID); err != nil {
		return res, err
	}
	type dependent struct {
		id    string
		tasks []units.Unit
	}
	var waiting []dependent
	if opt.Dependents {
		for _, id := range res.Dependents {
			tasks, err := unfinishedTasks(ctx, u, id)
			if err != nil {
				return res, err
			}
			waiting = append(waiting, dependent{id, tasks})
			res.DependentTasks = append(res.DependentTasks, taskIDs(tasks)...)
		}
	}
	if !opt.Yes {
		return res, nil
	}

	reason := fmt.Sprintf("pull request %s was closed without merging; slice cancelled", rec.URL)
	if err := cancelTasks(ctx, u, own, reason); err != nil {
		return res, err
	}
	for _, w := range waiting {
		if err := cancelTasks(ctx, u, w.tasks, fmt.Sprintf("%s was cancelled; this slice waited for it", slice.ID)); err != nil {
			return res, err
		}
	}
	if err := u.ResolvePullRequest(ctx, slice.ID, rec.Number, "cancelled"); err != nil {
		return res, err
	}
	rec.Resolution = "cancelled"
	res.Resolved, res.Done = rec, true
	note := fmt.Sprintf("Pull request %s was closed without merging; slice cancelled.", rec.URL)
	if len(res.Tasks) > 0 {
		note += " Cancelled: " + strings.Join(res.Tasks, ", ") + "."
	}
	if _, err := u.AddNote(ctx, slice.ID, units.NoteGeneral, note); err != nil {
		return res, err
	}
	for _, w := range waiting {
		note := fmt.Sprintf("%s was cancelled; this slice waited for it.", slice.ID)
		if len(w.tasks) > 0 {
			note += " Cancelled: " + strings.Join(taskIDs(w.tasks), ", ") + "."
		}
		if _, err := u.AddNote(ctx, w.id, units.NoteGeneral, note); err != nil {
			return res, err
		}
	}
	return res, nil
}
