package quick

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
)

const maxTitle = 200

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", units.ErrInvalid, fmt.Sprintf(format, args...))
}

func validateTitle(raw string) (string, error) {
	title := strings.TrimSpace(raw)
	switch {
	case title == "":
		return "", invalid("the title is empty")
	case strings.ContainsAny(title, "\r\n"):
		return "", invalid("the title must be one line")
	case len([]rune(title)) > maxTitle:
		return "", invalid("the title has %d characters; the most allowed is %d", len([]rune(title)), maxTitle)
	}
	return title, nil
}

func afterBranchError(branch string, cause, cancelErr error) error {
	err := fmt.Errorf("quick work: %w; the branch %s was created and was left in place", cause, branch)
	if cancelErr != nil {
		return errors.Join(err, fmt.Errorf("quick work: cancel the task: %w", cancelErr))
	}
	return err
}

var (
	// ErrNoIssueSource means a ticket was named and no issue source is configured.
	ErrNoIssueSource = errors.New("no issue source is configured: set issues.provider to jira")
	// ErrNotQuickWork means the task asked for is not an in-progress orphan task with a branch.
	ErrNotQuickWork = errors.New("not quick work in progress")
	// ErrAmbiguousTask means more than one quick work task matches.
	ErrAmbiguousTask = errors.New("more than one quick work task matches")
	// ErrWrongBranch means the checkout is not on the task's branch.
	ErrWrongBranch = errors.New("the checkout is not on the task's branch")
	// ErrNothingToDeliver means the branch has no changes and no commits of its own.
	ErrNothingToDeliver = errors.New("nothing to deliver")
	// ErrVerificationFailed means a verification command failed or timed out.
	ErrVerificationFailed = errors.New("verification failed")
	// ErrCommitFailed means git could not make the commit.
	ErrCommitFailed = errors.New("commit failed")
	// ErrPushFailed means the branch could not be pushed.
	ErrPushFailed = errors.New("push failed")
	// ErrPullRequestFailed means the pull request could not be found or opened.
	ErrPullRequestFailed = errors.New("pull request failed")
)

// VerificationError carries the report of a verification run that did not pass.
type VerificationError struct {
	Report verify.Report
}

func (e *VerificationError) Error() string {
	res, ok := e.Report.Failed()
	if !ok {
		return ErrVerificationFailed.Error()
	}
	switch {
	case res.TimedOut:
		return fmt.Sprintf("%s: %q timed out", ErrVerificationFailed, res.Command)
	case res.StartErr != "":
		return fmt.Sprintf("%s: %q could not start: %s", ErrVerificationFailed, res.Command, res.StartErr)
	}
	return fmt.Sprintf("%s: %q exited with code %d", ErrVerificationFailed, res.Command, res.ExitCode)
}

func (e *VerificationError) Is(target error) bool { return target == ErrVerificationFailed }
