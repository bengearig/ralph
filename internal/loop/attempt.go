package loop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/headless"
	"github.com/bengearig/ralph/internal/issuelink"
	"github.com/bengearig/ralph/internal/naming"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/bengearig/ralph/internal/workspace"
)

// ErrBranchChanged means the checkout left the slice's branch during an attempt.
var ErrBranchChanged = errors.New("the branch changed during the attempt")

const (
	commitLinkPrefix = "commit:"
	shortHash        = 12
	noChangesFailure = "the agent made no changes"
	noVerifyWarning  = "no verification commands configured: the result was not checked"
)

// Outcome is how an attempt ended.
type Outcome string

const (
	OutcomeDone    Outcome = "done"
	OutcomeFailed  Outcome = "failed"
	OutcomeAborted Outcome = "aborted"
)

// AttemptInput says which attempt to make.
type AttemptInput struct {
	Task, Slice units.Unit
	// Branch is the slice's branch, which the checkout must stay on.
	Branch      string
	Attempt     int
	MaxAttempts int
	Previous    *Failure
	// DocumentBytes caps linked-document text in the prompt; 0 means no limit.
	DocumentBytes int
}

// AttemptResult is what one attempt did.
type AttemptResult struct {
	Outcome      Outcome
	Failure      string
	Commit       string
	Prompt       Prompt
	Run          headless.Result
	Verification verify.Report
	StartHead    string
	EndHead      string
	Duration     time.Duration
	RecordPath   string
	RecordErrors []string
	Denials      []harness.Denial
}

type attempt struct {
	d     Deps
	in    AttemptInput
	res   AttemptResult
	rec   AttemptRecord
	start time.Time
}

// Attempt runs one attempt on one task: the harness, verification and the commit.
func Attempt(ctx context.Context, d Deps, in AttemptInput) (AttemptResult, error) {
	a := &attempt{d: d, in: in, start: time.Now()}
	a.res.Outcome = OutcomeAborted
	if a.d.Recorder == nil {
		a.d.Recorder = noopRecorder{}
	}
	err := a.run(ctx)
	return a.res, err
}

func (a *attempt) emit(e Event) {
	if a.d.Report == nil {
		return
	}
	e.Task, e.Attempt, e.Max = a.in.Task.ID, a.in.Attempt, a.in.MaxAttempts
	a.d.Report(e)
}

func (a *attempt) recordErr(what string, err error) {
	if err != nil {
		a.res.RecordErrors = append(a.res.RecordErrors, what+": "+err.Error())
	}
}

func (a *attempt) setState(ctx context.Context, to units.State, reason string) error {
	cur, err := a.d.Units.Get(ctx, a.in.Task.ID)
	if err != nil {
		return err
	}
	_, err = a.d.Units.SetState(ctx, cur.ID, cur.Fingerprint, to, reason)
	return err
}

func (a *attempt) run(ctx context.Context) error {
	d, in := a.d, a.in
	task, err := d.Units.Get(ctx, in.Task.ID)
	if err != nil {
		return err
	}
	if task.State == units.StateTodo {
		if err := a.setState(ctx, units.StateInProgress, fmt.Sprintf("loop attempt %d", in.Attempt)); err != nil {
			return err
		}
	}
	st, err := a.state(ctx)
	if err != nil {
		return err
	}
	if st.Branch != in.Branch {
		return branchChanged(in.Branch, st.Branch)
	}
	a.res.StartHead = st.Head
	a.emit(Event{Kind: EventAttemptStarted})

	prompt, err := BuildPrompt(ctx, d.Prompt, PromptInput{
		TaskID: in.Task.ID, Verify: d.Verify.Commands, Attempt: in.Attempt, MaxAttempts: in.MaxAttempts,
		Previous: in.Previous, DocumentBytes: in.DocumentBytes,
	})
	if err != nil {
		return err
	}
	a.res.Prompt = prompt

	a.rec, err = d.Recorder.Begin(ctx, AttemptInfo{
		Slice: in.Slice.ID, Task: in.Task.ID, Attempt: in.Attempt, Harness: harnessName(d.Adapter),
		Model: d.Model, Mode: d.Mode, StartHead: st.Head, Started: a.start,
	})
	if err != nil || a.rec == nil {
		a.recordErr("begin the record", err)
		a.rec = noopRecord{}
	}
	a.recordErr("write the prompt", a.rec.Prompt(prompt.Text))

	req, err := headless.NewRequest(d.Config, d.Root, prompt.Text, d.MCP)
	if err != nil {
		return a.abort(err)
	}
	req.Model, req.Version = d.Model, d.HarnessVersion
	if d.Mode != "" {
		req.Mode = d.Mode
		if d.Mode == harness.PermissionAuto {
			req.Allow.Commands, req.Allow.Tools = nil, nil
		}
	}
	opt, err := headless.NewOptions(d.Config)
	if err != nil {
		return a.abort(err)
	}
	opt.Stdout = a.rec.Stdout()
	execute := d.Execute
	if execute == nil {
		execute = headless.Execute
	}
	run, err := execute(ctx, d.Adapter, req, opt)
	a.res.Run = run
	if err != nil {
		return a.abort(err)
	}
	if run.Canceled {
		bg := context.WithoutCancel(ctx)
		if end, serr := d.Workspace.State(bg); serr == nil {
			a.res.EndHead = end.Head
		}
		serr := a.setState(bg, units.StateTodo, "loop interrupted")
		a.finishRecord()
		if serr != nil {
			return errors.Join(context.Canceled, serr)
		}
		return context.Canceled
	}

	end, err := a.state(ctx)
	if err != nil {
		return a.abort(err)
	}
	a.res.EndHead = end.Head
	if end.Branch != in.Branch {
		return a.abort(branchChanged(in.Branch, end.Branch))
	}

	failure := run.Failure()
	a.res.Denials = run.Outcome.Denials
	if failure == "" {
		failure, err = a.verify(ctx)
		if err != nil {
			return a.abort(err)
		}
	}
	if failure == "" {
		failure, err = a.commit(ctx, end.Head, len(end.Changes) > 0)
		if err != nil {
			return a.abort(err)
		}
	}
	return a.conclude(ctx, failure)
}

// state reads the checkout; a detached HEAD is a state with no branch.
func (a *attempt) state(ctx context.Context) (workspace.State, error) {
	st, err := a.d.Workspace.State(ctx)
	if errors.Is(err, workspace.ErrDetachedHead) {
		return workspace.State{}, branchChanged(a.in.Branch, "")
	}
	return st, err
}

func branchChanged(want, got string) error {
	if got == "" {
		got = "no branch (detached HEAD)"
	}
	return fmt.Errorf("%w: expected %s, found %s", ErrBranchChanged, want, got)
}

func harnessName(a harness.Adapter) string {
	if a == nil {
		return ""
	}
	return a.Name()
}

// abort ends the attempt as aborted, leaving the task's state alone.
func (a *attempt) abort(err error) error {
	a.res.Outcome = OutcomeAborted
	a.finishRecord()
	return err
}

func (a *attempt) finishRecord() {
	if a.rec == nil {
		return
	}
	a.res.Duration = time.Since(a.start)
	path, err := a.rec.Finish(a.res, a.res.Run)
	a.recordErr("finish the record", err)
	a.res.RecordPath = path
}

func (a *attempt) verify(ctx context.Context) (string, error) {
	cmds := a.d.Verify.Commands
	if len(cmds) == 0 {
		a.emit(Event{Kind: EventWarning, Message: noVerifyWarning})
		return "", nil
	}
	report, err := a.d.Verifier.Run(ctx, cmds)
	if err != nil {
		if ctx.Err() != nil {
			a.res.Verification = report
			a.recordErr("write the verification", a.rec.Verification(report))
			serr := a.setState(context.WithoutCancel(ctx), units.StateTodo, "loop interrupted")
			return "", errors.Join(ctx.Err(), serr)
		}
		return "", err
	}
	a.res.Verification = report
	a.recordErr("write the verification", a.rec.Verification(report))
	if f, failed := report.Failed(); failed {
		how := fmt.Sprintf("exit %d", f.ExitCode)
		if f.TimedOut {
			how = "timed out"
		}
		return fmt.Sprintf("verification failed: %s (%s)", f.Command, how), nil
	}
	return "", nil
}

// commit decides which commit is the work. It returns a failure line when
// there is nothing to commit.
func (a *attempt) commit(ctx context.Context, head string, dirty bool) (string, error) {
	if head != a.res.StartHead {
		a.res.Commit = head
		return "", nil
	}
	if !dirty {
		return noChangesFailure, nil
	}
	task, in := a.in.Task, a.in
	ticket, _, err := issuelink.Of(ctx, a.d.Units, task.ID)
	if err != nil {
		return "", fmt.Errorf("find the ticket of %s: %w", task.ID, err)
	}
	subject, err := naming.RenderSubject(a.d.CommitTemplate, naming.Data{
		ID: task.ID, Level: "task", Number: task.Number, Title: task.Title,
		Slug: naming.Slugify(task.Title), ParentID: in.Slice.ID, Ticket: ticket.Key,
	})
	if err != nil {
		return "", fmt.Errorf("render the commit subject: %w", err)
	}
	hash, err := a.d.Workspace.CommitAll(ctx, naming.FormatCommitMessage(subject, task.ID, true))
	if err != nil {
		return "", fmt.Errorf("commit the work: %w; the changes are left staged", err)
	}
	a.res.Commit, a.res.EndHead = hash, hash
	return "", nil
}

func (a *attempt) conclude(ctx context.Context, failure string) error {
	in, task := a.in, a.in.Task
	a.res.Failure = failure
	a.res.Outcome = OutcomeDone
	if failure != "" {
		a.res.Outcome = OutcomeFailed
	}
	a.finishRecord()

	body := AttemptSummary(in.Attempt, in.MaxAttempts, a.res, SecretValues(a.d.Config))
	if failure == "" {
		if err := a.d.Units.AddLink(ctx, task.ID, units.LinkExternal, commitLinkPrefix+a.res.Commit); err != nil {
			return a.fail(err)
		}
	}
	if _, err := a.d.Units.AddNote(ctx, task.ID, units.NoteGeneral, body); err != nil {
		return a.fail(err)
	}
	if failure != "" {
		a.emit(Event{Kind: EventAttemptFailed, Message: failure, RecordPath: a.res.RecordPath})
		return nil
	}
	if err := a.setState(ctx, units.StateDone, "loop: verified and committed "+short(a.res.Commit)); err != nil {
		return a.fail(err)
	}
	e := Event{Kind: EventTaskDone, Commit: a.res.Commit, Duration: a.res.Duration, RecordPath: a.res.RecordPath}
	if u := a.res.Run.Outcome.Usage; u.CostReported {
		e.CostUSD = u.CostUSD
	}
	a.emit(e)
	return nil
}

func (a *attempt) fail(err error) error {
	a.res.Outcome = OutcomeAborted
	return err
}

func short(hash string) string {
	if len(hash) > shortHash {
		return hash[:shortHash]
	}
	return hash
}
