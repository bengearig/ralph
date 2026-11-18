package quick

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/issuelink"
	"github.com/bengearig/ralph/internal/naming"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
)

const branchLinkPrefix = "branch:"

// FinishInput says which work to deliver. An empty ID means the task on the current branch.
type FinishInput struct {
	ID string
	// Summary is what changed, as the agent describes it.
	Summary string
}

// Finished is the result of Finish.
type Finished struct {
	Task   units.Unit
	Branch string
	Base   string
	// Commit is the full hash of the delivered commit: the new one, or HEAD when the work was already committed.
	Commit string
	// Committed is whether this call made the commit.
	Committed              bool
	PullRequest            hosting.PullRequest
	PullRequestReused      bool
	Verification           verify.Report
	VerificationConfigured bool
	Verify                 verify.Settings
}

// Finish verifies the work, commits it, pushes the branch, opens the pull
// request and marks the task done. It never merges. Whatever fails after the
// commit leaves the task in progress and the commit in place, so calling it
// again resumes.
func (s *Service) Finish(ctx context.Context, in FinishInput) (Finished, error) {
	cfg := s.deps.Config
	ws := s.deps.Workspace
	if err := ctx.Err(); err != nil {
		return Finished{}, err
	}
	st, err := ws.State(ctx)
	if err != nil {
		return Finished{}, err
	}
	task, branch, err := s.findTask(ctx, in.ID, st.Branch)
	if err != nil {
		return Finished{}, err
	}
	if st.Branch != branch {
		return Finished{}, fmt.Errorf("%w: %s is on branch %s but %s is checked out: run git switch %s first",
			ErrWrongBranch, task.ID, branch, orDetached(st.Branch), branch)
	}

	base := hosting.BaseBranch(ctx, cfg.Root, cfg.Remote, cfg.Base)
	dirty := len(st.Changes) > 0
	if !dirty {
		ahead, err := ws.CommitsAhead(ctx, base)
		if err != nil {
			return Finished{}, err
		}
		if ahead == 0 {
			return Finished{}, fmt.Errorf("%w: %s has no changes and no commits that %s lacks", ErrNothingToDeliver, branch, base)
		}
	}

	out := Finished{Task: task, Branch: branch, Base: base, Verify: cfg.Verify,
		VerificationConfigured: len(cfg.Verify.Commands) > 0}
	if out.VerificationConfigured {
		report, err := s.runner().Run(ctx, cfg.Verify.Commands)
		if err != nil {
			return Finished{}, err
		}
		if !report.Passed() {
			return Finished{}, &VerificationError{Report: report}
		}
		out.Verification = report
	}

	ticket, hasTicket, err := issuelink.Of(ctx, s.deps.Units, task.ID)
	if err != nil {
		return Finished{}, fmt.Errorf("quick work: find the ticket of %s: %w", task.ID, err)
	}
	subject, err := naming.RenderSubject(cfg.CommitTemplate, naming.Data{
		ID: task.ID, Level: "task", Number: task.Number, Title: task.Title, Slug: naming.Slugify(task.Title),
		Ticket: ticket.Key,
	})
	if err != nil {
		return Finished{}, fmt.Errorf("quick work: render the commit subject: %w", err)
	}
	if dirty {
		hash, err := ws.CommitAll(ctx, naming.FormatCommitMessage(subject, task.ID, cfg.CommitTrailer))
		if err != nil {
			return Finished{}, fmt.Errorf("%w: %w; the changes are left staged", ErrCommitFailed, err)
		}
		out.Commit, out.Committed = hash, true
	} else {
		out.Commit = st.Head
	}

	if err := ws.Push(ctx, cfg.Remote, branch); err != nil {
		return Finished{}, fmt.Errorf("%w: %w; the commit is kept, call finish again to retry", ErrPushFailed, err)
	}

	bodyIn := BodyInput{
		ID: task.ID, Body: task.Body, AcceptanceCriteria: task.AcceptanceCriteria, Summary: in.Summary,
		Report: out.Verification, Configured: out.VerificationConfigured,
	}
	if hasTicket {
		bodyIn.Issue = &BodyIssue{Key: ticket.Key, URL: IssueURL(cfg.JiraSite, ticket.Key)}
	}
	body := BuildPullRequestBody(bodyIn)
	pr, reused, err := s.openPullRequest(ctx, hosting.NewPullRequest{
		Head: branch, Base: base, Title: subject, Body: body, Draft: cfg.Draft,
	})
	if err != nil {
		return Finished{}, err
	}
	out.PullRequest, out.PullRequestReused = pr, reused

	out.Task, err = s.record(ctx, task.ID, pr.URL, reused)
	if err != nil {
		return Finished{}, fmt.Errorf("quick work: the pull request exists at %s but recording it failed: %w", pr.URL, err)
	}
	return out, nil
}

func orDetached(branch string) string {
	if branch == "" {
		return "no branch (detached HEAD)"
	}
	return branch
}

func (s *Service) runner() Runner {
	if s.deps.Runner != nil {
		return s.deps.Runner
	}
	return verify.Runner{Dir: s.deps.Config.Root, Timeout: s.deps.Config.Verify.Timeout}
}

func (s *Service) findTask(ctx context.Context, id, current string) (units.Unit, string, error) {
	u := s.deps.Units
	if id != "" {
		task, err := u.Get(ctx, id)
		if err != nil {
			return units.Unit{}, "", err
		}
		switch {
		case task.Level != units.LevelTask:
			return units.Unit{}, "", fmt.Errorf("%w: %s is not a task", ErrNotQuickWork, id)
		case task.ParentID != "":
			return units.Unit{}, "", fmt.Errorf("%w: %s belongs to %s", ErrNotQuickWork, id, task.ParentID)
		case task.State != units.StateInProgress:
			return units.Unit{}, "", fmt.Errorf("%w: %s is %s, not IN_PROGRESS", ErrNotQuickWork, id, task.State)
		}
		branch, err := s.branchOf(ctx, id)
		if err != nil {
			return units.Unit{}, "", err
		}
		if branch == "" {
			return units.Unit{}, "", fmt.Errorf("%w: %s has no branch link", ErrNotQuickWork, id)
		}
		return task, branch, nil
	}

	noParent := ""
	list, err := u.List(ctx, units.Filter{Level: units.LevelTask, ParentID: &noParent, States: []units.State{units.StateInProgress}})
	if err != nil {
		return units.Unit{}, "", err
	}
	var matches []units.Unit
	for _, t := range list {
		branch, err := s.branchOf(ctx, t.ID)
		if err != nil {
			return units.Unit{}, "", err
		}
		if branch != "" && branch == current {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 0:
		return units.Unit{}, "", fmt.Errorf("%w: no in-progress quick work task is on branch %s", ErrNotQuickWork, orDetached(current))
	case 1:
		return matches[0], current, nil
	}
	ids := make([]string, len(matches))
	for i, m := range matches {
		ids[i] = m.ID
	}
	return units.Unit{}, "", fmt.Errorf("%w: %s all use branch %s; pass the ID of the one to finish",
		ErrAmbiguousTask, strings.Join(ids, ", "), current)
}

// branchOf returns the branch a task's link names, or "" when it has none.
func (s *Service) branchOf(ctx context.Context, id string) (string, error) {
	return branchLink(ctx, s.deps.Units, id)
}

func (s *Service) openPullRequest(ctx context.Context, n hosting.NewPullRequest) (hosting.PullRequest, bool, error) {
	fail := func(err error) (hosting.PullRequest, bool, error) {
		return hosting.PullRequest{}, false, fmt.Errorf("%w: %w; the branch is pushed, call finish again to retry", ErrPullRequestFailed, err)
	}
	if s.deps.Provider == nil {
		return fail(errors.New("no pull request provider is configured"))
	}
	p, err := s.deps.Provider(ctx)
	if err != nil {
		return fail(err)
	}
	find := func() (hosting.PullRequest, bool, error) {
		pr, ok, err := p.FindPullRequest(ctx, n.Head)
		return pr, ok && pr.State == hosting.StateOpen, err
	}
	pr, open, err := find()
	if err != nil {
		return fail(err)
	}
	if open {
		return pr, true, nil
	}
	pr, err = p.CreatePullRequest(ctx, n)
	if errors.Is(err, hosting.ErrConflict) {
		again, open, ferr := find()
		if ferr != nil {
			return fail(ferr)
		}
		if open {
			return again, true, nil
		}
	}
	if err != nil {
		return fail(err)
	}
	return pr, false, nil
}

func (s *Service) record(ctx context.Context, id, url string, reused bool) (units.Unit, error) {
	u := s.deps.Units
	if err := u.AddLink(ctx, id, units.LinkExternal, "pr:"+url); err != nil {
		return units.Unit{}, err
	}
	verb := "opened"
	if reused {
		verb = "reused"
	}
	if _, err := u.AddNote(ctx, id, units.NoteGeneral, fmt.Sprintf("Pull request %s: %s", verb, url)); err != nil {
		return units.Unit{}, err
	}
	cur, err := u.Get(ctx, id)
	if err != nil {
		return units.Unit{}, err
	}
	return u.SetState(ctx, id, cur.Fingerprint, units.StateDone, "quick work delivered in "+url)
}
