package loop

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/bengearig/ralph/internal/units"
)

const (
	verificationOutputChars = 4000
	ellipsis                = "…"
)

// TaskOutcome is how work on a task ended.
type TaskOutcome string

const (
	TaskDone    TaskOutcome = "done"
	TaskStalled TaskOutcome = "stalled"
	TaskAborted TaskOutcome = "aborted"
)

// TaskInput says which task to work on and how many failures to bear.
type TaskInput struct {
	Task, Slice units.Unit
	// Branch is the slice's branch, which the checkout must stay on.
	Branch string
	// MaxFailures is the number of consecutive failed attempts that stalls the task.
	MaxFailures int
	// DocumentBytes caps linked-document text in the prompt; 0 means no limit.
	DocumentBytes int
}

// TaskResult is what working on a task did.
type TaskResult struct {
	Outcome  TaskOutcome
	Attempts []AttemptResult
	// Reason is why the task stalled; empty otherwise.
	Reason string
}

// RunTask attempts a task until it is done, stalls, or a hard stop ends it.
// A hard stop is not a failed attempt: its error is returned as it came.
func RunTask(ctx context.Context, d Deps, in TaskInput) (TaskResult, error) {
	if in.MaxFailures < 1 {
		return TaskResult{}, fmt.Errorf("the failure limit must be at least 1, got %d", in.MaxFailures)
	}
	res := TaskResult{Outcome: TaskAborted}
	var previous *Failure
	for n := 1; n <= in.MaxFailures; n++ {
		att, err := Attempt(ctx, d, AttemptInput{
			Task: in.Task, Slice: in.Slice, Branch: in.Branch, Attempt: n, MaxAttempts: in.MaxFailures,
			Previous: previous, DocumentBytes: in.DocumentBytes,
		})
		res.Attempts = append(res.Attempts, att)
		if err != nil {
			return res, err
		}
		if att.Outcome == OutcomeDone {
			res.Outcome = TaskDone
			return res, nil
		}
		previous = failureOf(att)
	}
	last := res.Attempts[len(res.Attempts)-1]
	res.Reason = newRedactor(SecretValues(d.Config)).String(
		fmt.Sprintf("stalled after %d failed attempts: %s", in.MaxFailures, last.Failure))
	if err := stall(ctx, d, in.Task.ID, res.Reason); err != nil {
		return res, err
	}
	res.Outcome = TaskStalled
	if d.Report != nil {
		d.Report(Event{Kind: EventTaskStalled, Task: in.Task.ID, Attempt: in.MaxFailures, Max: in.MaxFailures, Message: res.Reason})
	}
	return res, nil
}

func stall(ctx context.Context, d Deps, id, reason string) error {
	cur, err := d.Units.Get(ctx, id)
	if err != nil {
		return err
	}
	if _, err := d.Units.SetState(ctx, cur.ID, cur.Fingerprint, units.StateBlocked, reason); err != nil {
		return fmt.Errorf("block the stalled task: %w", err)
	}
	return nil
}

func failureOf(a AttemptResult) *Failure {
	f := &Failure{Line: a.Failure, Denials: a.Denials}
	if r, failed := a.Verification.Failed(); failed {
		f.VerificationOutput = lastChars(r.Output, verificationOutputChars)
	}
	return f
}

// lastChars keeps the last n characters of s, marking a cut with a leading ellipsis.
func lastChars(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return ellipsis + string(r[len(r)-n:])
}
