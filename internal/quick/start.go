package quick

import (
	"context"
	"errors"
	"fmt"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/naming"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/bengearig/ralph/internal/workspace"
)

// StartInput describes the work to start.
type StartInput struct {
	Title              string
	Body               string
	AcceptanceCriteria string
}

// Started is the result of Start.
type Started struct {
	Task   units.Unit
	Branch string
	Base   string
	// StartedFrom is the full hash of the commit the branch was created at.
	StartedFrom string
	Verify      verify.Settings
}

// Start creates an orphan task and its branch, and leaves the task in progress.
// A refusal by the workspace checks creates nothing.
func (s *Service) Start(ctx context.Context, in StartInput) (Started, error) {
	title, err := validateTitle(in.Title)
	if err != nil {
		return Started{}, err
	}
	body := in.Body
	if body == "" {
		body = title
	}
	cfg := s.deps.Config

	base := hosting.BaseBranch(ctx, cfg.Root, cfg.Remote, cfg.Base)
	st, err := workspace.CheckStartable(ctx, s.deps.Workspace, workspace.GuardOptions{Base: base, Remote: cfg.Remote})
	if err != nil {
		return Started{}, err
	}

	task, err := s.deps.Units.Create(ctx, units.NewUnit{
		Level: units.LevelTask, Title: title, Body: body, AcceptanceCriteria: in.AcceptanceCriteria,
	})
	if err != nil {
		return Started{}, err
	}
	id := task.ID
	task, err = s.deps.Units.SetState(ctx, id, task.Fingerprint, units.StateTodo, "quick work")
	if err != nil {
		return Started{}, s.cancelBefore(ctx, id, "quick work could not be prepared", err)
	}

	branch, err := naming.RenderBranch(cfg.BranchTemplate, naming.Data{
		ID: task.ID, Level: "task", Number: task.Number, Title: title, Slug: naming.Slugify(title),
	})
	if err != nil {
		return Started{}, s.cancelBefore(ctx, id, "the branch name could not be rendered", err)
	}
	exists, err := s.deps.Workspace.BranchExists(ctx, branch)
	if err != nil {
		return Started{}, s.cancelBefore(ctx, id, "could not look for branch "+branch, err)
	}
	if exists {
		return Started{}, s.cancelBefore(ctx, id, "branch "+branch+" already exists",
			fmt.Errorf("%w: %s", workspace.ErrBranchExists, branch))
	}
	if err := s.deps.Workspace.CreateBranch(ctx, branch); err != nil {
		return Started{}, s.cancelBefore(ctx, id, "could not create branch "+branch, err)
	}

	task, err = s.finishStart(ctx, task, branch, base, st.Head)
	if err != nil {
		return Started{}, err
	}
	return Started{Task: task, Branch: branch, Base: base, StartedFrom: st.Head, Verify: cfg.Verify}, nil
}

func (s *Service) finishStart(ctx context.Context, task units.Unit, branch, base, head string) (units.Unit, error) {
	u := s.deps.Units
	id := task.ID
	short := head
	if len(short) > 12 {
		short = short[:12]
	}
	steps := []func() error{
		func() error { return u.AddLink(ctx, id, units.LinkExternal, "branch:"+branch) },
		func() error {
			_, err := u.AddNote(ctx, id, units.NoteGeneral,
				fmt.Sprintf("Quick work started on branch %s from %s (base %s).", branch, short, base))
			return err
		},
		func() error {
			var err error
			task, err = u.SetState(ctx, id, task.Fingerprint, units.StateInProgress, "quick work started on "+branch)
			return err
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return units.Unit{}, afterBranchError(branch, err, s.cancel(ctx, id, "quick work failed after creating branch "+branch))
		}
	}
	return task, nil
}

// cancelBefore cancels the task and returns the cause, joined with any failure to cancel.
func (s *Service) cancelBefore(ctx context.Context, id, reason string, cause error) error {
	err := fmt.Errorf("quick work: %s: %w", reason, cause)
	if cerr := s.cancel(ctx, id, reason); cerr != nil {
		return errors.Join(err, fmt.Errorf("quick work: cancel the task: %w", cerr))
	}
	return err
}

func (s *Service) cancel(ctx context.Context, id, reason string) error {
	cur, err := s.deps.Units.Get(ctx, id)
	if err != nil {
		return err
	}
	_, err = s.deps.Units.SetState(ctx, id, cur.Fingerprint, units.StateCancelled, reason)
	return err
}
