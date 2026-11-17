package followup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/workspace"
)

// CloseResult says what Close did and what it refused to do.
type CloseResult struct {
	Slice string
	// Lines report every step, refusals included.
	Lines       []string
	Deleted     []string
	Refused     []string
	BaseUpdated bool
}

// Close finishes a merged slice: it deletes its branches when their tips are
// the commit the merged pull request had, brings the base branch up to date,
// and notes what it did on the slice. The caller has recorded the merge. A
// refusal is reported in the result and is not an error.
func Close(ctx context.Context, d Deps, o Observation) (CloseResult, error) {
	res := CloseResult{Slice: o.Slice.ID}
	mode := d.DeleteBranches
	if mode == "" {
		mode = DeleteLocal
	}
	if mode == DeleteNone {
		return res, nil
	}
	if d.Workspace == nil {
		return res, errors.New("no workspace is configured")
	}
	refuse := func(format string, a ...any) {
		line := fmt.Sprintf(format, a...)
		res.Refused = append(res.Refused, line)
		res.Lines = append(res.Lines, line)
	}
	head := o.PullRequest.HeadCommit
	if head == "" {
		refuse("cannot tell whether %s holds unmerged work: the host did not report the pull request's head commit", o.LocalBranch)
		return res, nil
	}
	if err := closeLocal(ctx, d, o, head, &res, refuse); err != nil {
		return res, err
	}
	if mode == DeleteBoth {
		if err := closeRemote(ctx, d, o, head, &res, refuse); err != nil {
			return res, err
		}
	}
	if err := updateBase(ctx, d, &res); err != nil {
		return res, err
	}
	if len(res.Deleted) > 0 || res.BaseUpdated {
		url := o.Record.URL
		if url == "" {
			url = o.PullRequest.URL
		}
		body := fmt.Sprintf("Merged: %s; %s", url, strings.Join(res.Lines, "; "))
		if _, err := d.Units.AddNote(ctx, o.Slice.ID, units.NoteGeneral, body); err != nil {
			return res, err
		}
	}
	return res, nil
}

func closeLocal(ctx context.Context, d Deps, o Observation, head string, res *CloseResult, refuse func(string, ...any)) error {
	branch := o.LocalBranch
	tip, exists, err := d.Workspace.BranchTip(ctx, branch)
	if err != nil {
		return fmt.Errorf("look for branch %s: %w", branch, err)
	}
	if !exists {
		return nil
	}
	st, err := d.Workspace.State(ctx)
	switch {
	case err == nil:
		if st.Branch == branch {
			refuse("%s is checked out: git switch %s first", branch, d.PR.Config.Base)
			return nil
		}
	case errors.Is(err, workspace.ErrDetachedHead), errors.Is(err, workspace.ErrNoCommits):
	default:
		return err
	}
	if tip != head {
		refuse("%s has commits that were not in the merged pull request; kept", branch)
		return nil
	}
	if err := d.Workspace.DeleteBranch(ctx, branch); err != nil {
		return fmt.Errorf("delete branch %s: %w", branch, err)
	}
	line := "deleted local branch " + branch
	res.Deleted = append(res.Deleted, line)
	res.Lines = append(res.Lines, line)
	return nil
}

func closeRemote(ctx context.Context, d Deps, o Observation, head string, res *CloseResult, refuse func(string, ...any)) error {
	remote, branch := d.PR.Config.Remote, o.LocalBranch
	tip, exists, err := d.Workspace.RemoteBranchTip(ctx, remote, branch)
	if err != nil {
		return fmt.Errorf("look for branch %s on %s: %w", branch, remote, err)
	}
	if !exists {
		return nil
	}
	if tip != head {
		refuse("remote branch %s has commits that were not in the merged pull request; kept", branch)
		return nil
	}
	if err := d.Workspace.DeleteRemoteBranch(ctx, remote, branch); err != nil {
		return fmt.Errorf("delete branch %s on %s: %w", branch, remote, err)
	}
	line := fmt.Sprintf("deleted remote branch %s/%s", remote, branch)
	res.Deleted = append(res.Deleted, line)
	res.Lines = append(res.Lines, line)
	return nil
}

func updateBase(ctx context.Context, d Deps, res *CloseResult) error {
	base, remote := d.PR.Config.Base, d.PR.Config.Remote
	skip := func(reason string) {
		res.Lines = append(res.Lines, fmt.Sprintf("did not update %s: %s", base, reason))
	}
	st, err := d.Workspace.State(ctx)
	switch {
	case errors.Is(err, workspace.ErrDetachedHead):
		skip("HEAD is detached")
		return nil
	case errors.Is(err, workspace.ErrNoCommits):
		skip("the repository has no commits")
		return nil
	case err != nil:
		return err
	}
	if st.Branch != base {
		skip("the checkout is on " + st.Branch)
		return nil
	}
	if len(st.Changes) > 0 {
		skip("the working tree has changes")
		return nil
	}
	changed, err := d.Workspace.FastForward(ctx, remote, base)
	if err != nil {
		var ge *workspace.GitError
		if errors.As(err, &ge) {
			skip(fmt.Sprintf("it cannot be fast-forwarded from %s (%s)", remote, strings.TrimSpace(ge.Output)))
			return nil
		}
		return err
	}
	if changed {
		res.BaseUpdated = true
		res.Lines = append(res.Lines, fmt.Sprintf("updated %s from %s", base, remote))
	} else {
		res.Lines = append(res.Lines, base+" is up to date")
	}
	return nil
}
