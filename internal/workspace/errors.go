package workspace

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	ErrDetachedHead     = errors.New("HEAD is detached: switch to a branch first")
	ErrNoCommits        = errors.New("the repository has no commits yet")
	ErrBranchExists     = errors.New("branch already exists")
	ErrInvalidBranch    = errors.New("invalid branch name")
	ErrNothingToCommit  = errors.New("nothing to commit")
	ErrNotOnBase        = errors.New("not on the base branch")
	ErrDirty            = errors.New("the working tree has changes")
	ErrBaseAhead        = errors.New("the base branch is ahead of its remote")
	ErrBranchCheckedOut = errors.New("the branch is checked out")
)

const maxOutput = 2000

var urlCredentials = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s@]*@`)

// GitError reports a git command that failed in a way with no sentinel.
type GitError struct {
	Args     []string
	ExitCode int
	// Output is git's combined output, without URL credentials, cut to its first 2000 bytes.
	Output string
}

func newGitError(args []string, exitCode int, output string) *GitError {
	return &GitError{Args: args, ExitCode: exitCode, Output: sanitize(output)}
}

func sanitize(output string) string {
	output = urlCredentials.ReplaceAllString(output, "$1")
	if len(output) <= maxOutput {
		return output
	}
	cut := maxOutput
	for cut > 0 && !utf8.RuneStart(output[cut]) {
		cut--
	}
	return output[:cut]
}

func (e *GitError) Error() string {
	name := ""
	if len(e.Args) > 0 {
		name = e.Args[0]
	}
	return "git " + name + " failed (exit " + strconv.Itoa(e.ExitCode) + "): " + e.Output
}

// NotOnBaseError reports that the checkout is not on the base branch.
type NotOnBaseError struct {
	Current, Base string
}

func (e *NotOnBaseError) Error() string {
	return `the current branch is "` + e.Current + `", but quick work must start from the base branch "` + e.Base +
		`": run git switch ` + e.Base + ` first`
}

func (e *NotOnBaseError) Is(target error) bool { return target == ErrNotOnBase }

// DirtyError reports changes that are not committed.
type DirtyError struct {
	Paths []string
}

const listedPaths = 10

func (e *DirtyError) Error() string {
	shown := e.Paths
	if len(shown) > listedPaths {
		shown = shown[:listedPaths]
	}
	msg := "the working tree has changes: " + strings.Join(shown, ", ")
	if more := len(e.Paths) - len(shown); more > 0 {
		msg += " and " + strconv.Itoa(more) + " more"
	}
	return msg + ": commit or stash these changes first"
}

func (e *DirtyError) Is(target error) bool { return target == ErrDirty }

// BaseAheadError reports that the base branch has commits its remote-tracking branch lacks.
type BaseAheadError struct {
	Remote, Base string
	Count        int
}

func (e *BaseAheadError) Error() string {
	return `"` + e.Base + `" has ` + strconv.Itoa(e.Count) + " commit(s) that " + e.Remote + "/" + e.Base +
		" does not; push them or reset the branch first"
}

func (e *BaseAheadError) Is(target error) bool { return target == ErrBaseAhead }
