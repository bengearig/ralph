package quick

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/issuelink"
	"github.com/bengearig/ralph/internal/issuesource"
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
	// Issue is a ticket key or address to start from; empty starts from no ticket.
	Issue string
}

// IssueInfo is the ticket work was started from.
type IssueInfo struct {
	Key, URL, Title, Type, Status string
}

// Started is the result of Start.
type Started struct {
	Task   units.Unit
	Branch string
	Base   string
	// StartedFrom is the full hash of the commit the branch was created at.
	StartedFrom string
	Verify      verify.Settings
	// Issue is the ticket the work was started from, nil when none.
	Issue *IssueInfo
}

// Start creates an orphan task and its branch, and leaves the task in progress.
// A refusal by the workspace checks creates nothing.
func (s *Service) Start(ctx context.Context, in StartInput) (Started, error) {
	if in.Issue == "" {
		if _, err := validateTitle(in.Title); err != nil {
			return Started{}, err
		}
	}
	cfg := s.deps.Config

	base := hosting.BaseBranch(ctx, cfg.Root, cfg.Remote, cfg.Base)
	st, err := workspace.CheckStartable(ctx, s.deps.Workspace, workspace.GuardOptions{Base: base, Remote: cfg.Remote})
	if err != nil {
		return Started{}, err
	}

	title, body := in.Title, in.Body
	var issue *issuesource.Issue
	if in.Issue != "" {
		got, err := s.readIssue(ctx, in.Issue)
		if err != nil {
			return Started{}, err
		}
		issue = &got
		if strings.TrimSpace(title) == "" {
			title = cutTitle(got.Title)
		}
		if body == "" && strings.TrimSpace(got.Body) != "" {
			body = got.Body
		}
	}
	title, err = validateTitle(title)
	if err != nil {
		return Started{}, err
	}
	if body == "" {
		body = title
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

	data := naming.Data{ID: task.ID, Level: "task", Number: task.Number, Title: title, Slug: naming.Slugify(title)}
	if issue != nil {
		td := s.deps.Tickets
		td.Units = s.deps.Units
		if err := issuelink.Attach(ctx, td, id, *issue); err != nil {
			return Started{}, s.cancelBefore(ctx, id, "the ticket "+issue.Ref.Key+" could not be linked", err)
		}
		data.Ticket = issue.Ref.Key
	}
	branch, err := naming.RenderBranch(cfg.BranchTemplate, data)
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

	task, err = s.finishStart(ctx, task, branch, base, st.Head, data.Ticket)
	if err != nil {
		return Started{}, err
	}
	res := Started{Task: task, Branch: branch, Base: base, StartedFrom: st.Head, Verify: cfg.Verify}
	if issue != nil {
		res.Issue = &IssueInfo{Key: issue.Ref.Key, URL: issue.URL, Title: issue.Title, Type: issue.Type, Status: issue.Status}
	}
	return res, nil
}

// readIssue parses and reads the ticket and checks that no unit holds it, before anything is created.
func (s *Service) readIssue(ctx context.Context, raw string) (issuesource.Issue, error) {
	if s.deps.Issues == nil {
		return issuesource.Issue{}, ErrNoIssueSource
	}
	ref, err := s.deps.Issues.Parse(raw)
	if err != nil {
		return issuesource.Issue{}, err
	}
	issue, err := s.deps.Issues.Get(ctx, ref)
	if err != nil {
		if !strings.Contains(err.Error(), ref.Key) {
			err = fmt.Errorf("read %s: %w", ref.Key, err)
		}
		return issuesource.Issue{}, err
	}
	td := s.deps.Tickets
	td.Units = s.deps.Units
	if err := issuelink.Check(ctx, td, "", ref); err != nil {
		return issuesource.Issue{}, err
	}
	return issue, nil
}

// cutTitle makes a ticket title fit the title limit, cutting at a word boundary.
func cutTitle(raw string) string {
	title := strings.Join(strings.Fields(raw), " ")
	runes := []rune(title)
	if len(runes) <= maxTitle {
		return title
	}
	cut := string(runes[:maxTitle])
	if runes[maxTitle] != ' ' {
		if i := strings.LastIndexByte(cut, ' '); i > 0 {
			cut = cut[:i]
		}
	}
	return strings.TrimSpace(cut)
}

func (s *Service) finishStart(ctx context.Context, task units.Unit, branch, base, head, ticket string) (units.Unit, error) {
	u := s.deps.Units
	id := task.ID
	short := head
	if len(short) > 12 {
		short = short[:12]
	}
	steps := []func() error{
		func() error { return u.AddLink(ctx, id, units.LinkExternal, "branch:"+branch) },
		func() error {
			text := fmt.Sprintf("Quick work started on branch %s from %s (base %s).", branch, short, base)
			if ticket != "" {
				text += " Started from " + ticket + "."
			}
			_, err := u.AddNote(ctx, id, units.NoteGeneral, text)
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
