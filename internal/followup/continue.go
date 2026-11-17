package followup

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/workspace"
)

// LinkAutoStarted marks a slice whose loop was started by a continue follow-up.
const LinkAutoStarted = "loop:auto-started"

// LoopRunner runs the loop for a slice and returns its exit status.
type LoopRunner func(ctx context.Context, sliceID string) (status int, err error)

// ContinueResult says what Continue did. Slice is the slice the loop was
// asked to run for, empty when Continue refused.
type ContinueResult struct {
	Slice  string
	Status int
	Lines  []string
}

// NextSlice returns the ID of the slice a continue follow-up after the merged
// slice would start, or "" when there is none: the slice of the same brief with
// the lowest position after the merged one that is READY, waits for nothing and
// has not been started this way before.
func NextSlice(ctx context.Context, svc units.Service, merged units.Unit) (string, error) {
	if merged.ParentID == "" {
		return "", nil
	}
	parent := merged.ParentID
	siblings, err := svc.List(ctx, units.Filter{Level: units.LevelSlice, ParentID: &parent})
	if err != nil {
		return "", err
	}
	sort.SliceStable(siblings, func(i, j int) bool {
		if siblings[i].Position != siblings[j].Position {
			return siblings[i].Position < siblings[j].Position
		}
		return siblings[i].Number < siblings[j].Number
	})
	for _, s := range siblings {
		if s.Position <= merged.Position {
			continue
		}
		status, err := svc.Status(ctx, s.ID)
		if err != nil {
			return "", err
		}
		if status != units.StatusReady {
			continue
		}
		waiting, err := svc.Waiting(ctx, s.ID)
		if err != nil {
			return "", err
		}
		if len(waiting) > 0 {
			continue
		}
		links, err := svc.Links(ctx, s.ID)
		if err != nil {
			return "", err
		}
		started := false
		for _, l := range links {
			if l.Kind == units.LinkExternal && l.Target == LinkAutoStarted {
				started = true
			}
		}
		if !started {
			return s.ID, nil
		}
	}
	return "", nil
}

// Continue starts the loop for the slice of a continue follow-up through run.
// It refuses, without marking the slice, unless the checkout is on the base
// branch, clean and up to date with the remote. The slice is marked before the
// loop runs and stays marked whatever the loop's result.
func Continue(ctx context.Context, d Deps, run LoopRunner, f Followup) (ContinueResult, error) {
	next := f.Next
	res := ContinueResult{}
	if next == "" {
		return res, fmt.Errorf("%s: the follow-up names no slice to start", f.Slice)
	}
	if d.Workspace == nil {
		return res, errors.New("no workspace is configured")
	}
	if run == nil {
		return res, errors.New("no loop runner")
	}
	refuse := func(reason string) (ContinueResult, error) {
		res.Lines = append(res.Lines, fmt.Sprintf("not starting %s: %s", next, reason))
		return res, nil
	}
	base, remote := d.PR.Config.Base, d.PR.Config.Remote
	st, err := d.Workspace.State(ctx)
	switch {
	case errors.Is(err, workspace.ErrDetachedHead):
		return refuse("HEAD is detached")
	case errors.Is(err, workspace.ErrNoCommits):
		return refuse("the repository has no commits")
	case err != nil:
		return res, err
	case st.Branch != base:
		return refuse((&workspace.NotOnBaseError{Current: st.Branch, Base: base}).Error())
	case len(st.Changes) > 0:
		return refuse((&workspace.DirtyError{Paths: st.Changes}).Error())
	}
	if _, err := d.Workspace.FastForward(ctx, remote, base); err != nil {
		var ge *workspace.GitError
		if !errors.As(err, &ge) {
			return res, err
		}
		return refuse(fmt.Sprintf("%s cannot be fast-forwarded from %s (%s)", base, remote, strings.TrimSpace(ge.Output)))
	}
	n, tracked, err := d.Workspace.AheadOfRemote(ctx, remote, base)
	if err != nil {
		return res, err
	}
	if tracked && n > 0 {
		return refuse((&workspace.BaseAheadError{Remote: remote, Base: base, Count: n}).Error())
	}

	if err := d.Units.AddLink(ctx, next, units.LinkExternal, LinkAutoStarted); err != nil {
		return res, err
	}
	body := fmt.Sprintf("Started automatically after %s was merged.", f.Slice)
	if _, err := d.Units.AddNote(ctx, next, units.NoteGeneral, body); err != nil {
		return res, err
	}
	res.Slice = next
	res.Status, err = run(ctx, next)
	return res, err
}
