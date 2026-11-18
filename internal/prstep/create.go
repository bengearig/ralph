package prstep

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/naming"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/bengearig/ralph/internal/workspace"
)

const commitLinkPrefix = "commit:"

// CreateInput says which slice to open the pull request for.
type CreateInput struct {
	SliceID string
	// skipDecision lets Reopen open a pull request for a slice that waits
	// for a decision; only Reopen sets it.
	skipDecision bool
}

// CreateResult is what Create did.
type CreateResult struct {
	PullRequest hosting.PullRequest
	// Reused is true when the pull request was already open.
	Reused bool
	Branch string
	Remote string
}

// NotSliceError reports a unit that is not a slice.
type NotSliceError struct {
	ID    string
	Level units.Level
}

func (e *NotSliceError) Error() string {
	return fmt.Sprintf("%s is a %s, not a slice: give ralph pr create the ID of a slice", e.ID, e.Level)
}

// NothingDoneError reports a slice with no finished task.
type NothingDoneError struct{ Slice string }

func (e *NothingDoneError) Error() string {
	return fmt.Sprintf("%s has no DONE task, so there is nothing to open a pull request for: run ralph loop %s first", e.Slice, e.Slice)
}

// UnfinishedError reports tasks that are neither DONE nor CANCELLED.
type UnfinishedError struct {
	Slice string
	Tasks []units.Unit
}

func (e *UnfinishedError) Error() string {
	parts := make([]string, len(e.Tasks))
	for i, t := range e.Tasks {
		parts[i] = fmt.Sprintf("%s (%s)", t.ID, t.State)
	}
	return fmt.Sprintf("%s has tasks that are not finished: %s: run ralph loop %s", e.Slice, strings.Join(parts, ", "), e.Slice)
}

// NotOnBranchError reports a checkout that is not on the slice's branch.
type NotOnBranchError struct {
	Slice, Branch, Found string
	// Missing is true when the slice's branch does not exist.
	Missing bool
}

func (e *NotOnBranchError) Error() string {
	if e.Missing {
		return fmt.Sprintf("branch %s does not exist: run ralph loop %s first", e.Branch, e.Slice)
	}
	found := e.Found
	if found == "" {
		found = "no branch (detached HEAD)"
	}
	return fmt.Sprintf("%s is on branch %s but %s is checked out: run git switch %s", e.Slice, e.Branch, found, e.Branch)
}

// VerificationError reports the first verification command that failed.
type VerificationError struct {
	Result verify.Result
}

func (e *VerificationError) Error() string {
	r := e.Result
	how := fmt.Sprintf("exit %d", r.ExitCode)
	switch {
	case r.TimedOut:
		how = "timed out"
	case r.StartErr != "":
		how = "could not start: " + r.StartErr
	}
	return fmt.Sprintf("verification failed: %s (%s); nothing was pushed", r.Command, how)
}

// Create pushes the slice's branch and opens its pull request, or publishes
// new commits to the one that is open. It never merges and never forces.
func Create(ctx context.Context, d Deps, in CreateInput) (CreateResult, error) {
	cfg := d.Config
	slice, err := d.Units.Get(ctx, in.SliceID)
	if err != nil {
		return CreateResult{}, err
	}
	if slice.Level != units.LevelSlice {
		return CreateResult{}, &NotSliceError{ID: slice.ID, Level: slice.Level}
	}
	if !in.skipDecision {
		if err := Decision(ctx, d.Units, slice.ID); err != nil {
			return CreateResult{}, err
		}
	}
	done, err := checkTasks(ctx, d.Units, slice)
	if err != nil {
		return CreateResult{}, err
	}
	branch, err := SliceBranch(ctx, d.Units, cfg.BranchTemplate, slice)
	if err != nil {
		return CreateResult{}, err
	}
	if err := checkCheckout(ctx, d.Workspace, slice.ID, branch); err != nil {
		return CreateResult{}, err
	}

	report, configured, err := runVerification(ctx, d)
	if err != nil {
		return CreateResult{}, err
	}

	if err := d.Workspace.Push(ctx, cfg.Remote, branch); err != nil {
		return CreateResult{}, fmt.Errorf("push %s to %s: %w", branch, cfg.Remote, err)
	}
	d.report(fmt.Sprintf("pushed %s to %s", branch, cfg.Remote))

	body, err := buildSliceBody(ctx, d, slice, done, report, configured)
	if err != nil {
		return CreateResult{}, pushedError(err)
	}
	ticket, err := SliceTicket(ctx, d.Units, slice)
	if err != nil {
		return CreateResult{}, pushedError(err)
	}
	subject, err := naming.RenderSubject(cfg.CommitTemplate, sliceData(slice, ticket))
	if err != nil {
		return CreateResult{}, pushedError(fmt.Errorf("render the pull request title: %w", err))
	}
	base := hosting.BaseBranch(ctx, cfg.Root, cfg.Remote, cfg.Base)
	pr, reused, err := openPullRequest(ctx, d, hosting.NewPullRequest{
		Head: branch, Base: base, Title: subject, Body: body, Draft: cfg.Draft,
	})
	if err != nil {
		return CreateResult{}, pushedError(err)
	}
	if pr.Head == "" {
		pr.Head = branch
	}
	if pr.Base == "" {
		pr.Base = base
	}
	if err := record(ctx, d.Units, slice.ID, pr, reused); err != nil {
		return CreateResult{}, fmt.Errorf("the pull request exists at %s but recording it failed: %w; the branch is pushed, run ralph pr create again to retry", pr.URL, err)
	}
	return CreateResult{PullRequest: pr, Reused: reused, Branch: branch, Remote: cfg.Remote}, nil
}

func pushedError(err error) error {
	return fmt.Errorf("%w; the branch is pushed; run ralph pr create again to retry", err)
}

// checkTasks returns the DONE tasks of the slice in position order.
func checkTasks(ctx context.Context, svc units.Service, slice units.Unit) ([]units.Unit, error) {
	tasks, err := svc.List(ctx, units.Filter{Level: units.LevelTask, ParentID: &slice.ID})
	if err != nil {
		return nil, err
	}
	var done, unfinished []units.Unit
	for _, t := range tasks {
		switch t.State {
		case units.StateDone:
			done = append(done, t)
		case units.StateCancelled:
		default:
			unfinished = append(unfinished, t)
		}
	}
	if len(done) == 0 {
		return nil, &NothingDoneError{Slice: slice.ID}
	}
	if len(unfinished) > 0 {
		return nil, &UnfinishedError{Slice: slice.ID, Tasks: unfinished}
	}
	return done, nil
}

func checkCheckout(ctx context.Context, ws workspace.Workspace, sliceID, branch string) error {
	st, err := ws.State(ctx)
	if err != nil {
		return err
	}
	if st.Branch != branch {
		exists, err := ws.BranchExists(ctx, branch)
		if err != nil {
			return err
		}
		return &NotOnBranchError{Slice: sliceID, Branch: branch, Found: st.Branch, Missing: !exists}
	}
	if len(st.Changes) > 0 {
		return &workspace.DirtyError{Paths: st.Changes}
	}
	return nil
}

func runVerification(ctx context.Context, d Deps) (verify.Report, bool, error) {
	commands := d.Config.Verify.Commands
	if len(commands) == 0 {
		d.report("verification: none configured")
		return verify.Report{}, false, nil
	}
	report, err := d.verifier().Run(ctx, commands)
	for _, r := range report.Results {
		status := "passed"
		if !r.Passed() {
			status = "failed"
		}
		d.report(fmt.Sprintf("verification: %s (%s)", r.Command, status))
	}
	if err != nil {
		return verify.Report{}, true, err
	}
	if failed, ok := report.Failed(); ok {
		return verify.Report{}, true, &VerificationError{Result: failed}
	}
	return report, true, nil
}

func buildSliceBody(ctx context.Context, d Deps, slice units.Unit, done []units.Unit, report verify.Report, configured bool) (string, error) {
	svc := d.Units
	in := BodyInput{SliceID: slice.ID, SliceBody: slice.Body, Report: report, Configured: configured}
	ticket, err := SliceTicket(ctx, svc, slice)
	if err != nil {
		return "", err
	}
	if ticket != "" {
		in.Issue = &quick.BodyIssue{Key: ticket, URL: quick.IssueURL(d.Config.JiraSite, ticket)}
	}
	if slice.ParentID != "" {
		brief, err := svc.Get(ctx, slice.ParentID)
		if err != nil {
			return "", err
		}
		in.BriefID, in.BriefTitle = brief.ID, brief.Title
	}
	for _, t := range done {
		links, err := svc.Links(ctx, t.ID)
		if err != nil {
			return "", err
		}
		bt := BodyTask{ID: t.ID, Title: t.Title}
		for _, l := range links {
			if l.Kind == units.LinkExternal {
				if hash, ok := strings.CutPrefix(l.Target, commitLinkPrefix); ok && hash != "" {
					bt.Commit = hash
				}
			}
		}
		in.Tasks = append(in.Tasks, bt)
	}
	return BuildBody(in), nil
}

func openPullRequest(ctx context.Context, d Deps, n hosting.NewPullRequest) (hosting.PullRequest, bool, error) {
	if d.Provider == nil {
		return hosting.PullRequest{}, false, errors.New("no pull request provider is configured")
	}
	p, err := d.Provider(ctx)
	if err != nil {
		return hosting.PullRequest{}, false, err
	}
	find := func() (hosting.PullRequest, bool, error) {
		pr, ok, err := p.FindPullRequest(ctx, n.Head)
		return pr, ok && pr.State == hosting.StateOpen, err
	}
	pr, open, err := find()
	if err != nil {
		return hosting.PullRequest{}, false, err
	}
	if open {
		return pr, true, nil
	}
	pr, err = p.CreatePullRequest(ctx, n)
	if errors.Is(err, hosting.ErrConflict) {
		again, open, ferr := find()
		if ferr != nil {
			return hosting.PullRequest{}, false, ferr
		}
		if open {
			return again, true, nil
		}
	}
	if err != nil {
		return hosting.PullRequest{}, false, err
	}
	return pr, false, nil
}

func record(ctx context.Context, svc units.Service, sliceID string, pr hosting.PullRequest, reused bool) error {
	if err := svc.RecordPullRequest(ctx, units.PullRequestRecord{
		SliceID: sliceID, Number: pr.Number, URL: pr.URL, Head: pr.Head, Base: pr.Base, State: units.PullRequestOpen,
	}); err != nil {
		return err
	}
	if err := svc.AddLink(ctx, sliceID, units.LinkExternal, "pr:"+pr.URL); err != nil {
		return err
	}
	verb := "opened"
	if reused {
		verb = "reused"
	}
	_, err := svc.AddNote(ctx, sliceID, units.NoteGeneral, fmt.Sprintf("Pull request %s: %s", verb, pr.URL))
	return err
}
