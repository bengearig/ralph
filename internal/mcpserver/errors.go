package mcpserver

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/workspace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	codeNotInitialized = "not_initialized"
	codeNotFound       = "not_found"
	codeInvalid        = "invalid"
	codeInvalidParent  = "invalid_parent"
	codeTransition     = "invalid_transition"
	codeStale          = "stale"
	codeNotReady       = "not_ready"
	codeReasonRequired = "reason_required"
	codeWaiting        = "waiting"
	codeInternal       = "internal"

	codeDirtyTree          = "dirty_tree"
	codeNotOnBase          = "not_on_base"
	codeBaseAhead          = "base_ahead"
	codeDetachedHead       = "detached_head"
	codeNoCommits          = "no_commits"
	codeBranchExists       = "branch_exists"
	codeNotQuickWork       = "not_quick_work"
	codeAmbiguousTask      = "ambiguous_task"
	codeWrongBranch        = "wrong_branch"
	codeNothingToDeliver   = "nothing_to_deliver"
	codeVerificationFailed = "verification_failed"
	codeCommitFailed       = "commit_failed"
	codePushFailed         = "push_failed"
	codePullRequestFailed  = "pull_request_failed"
)

var urlCredentials = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s@]*@`)

const notInitializedText = "ralph is not set up in this project: ask the user to run `ralph init` in the project directory, then try again."

// errorResult turns a Go error into a tool result with the error flag set and
// a structured error_code.
func errorResult(err error) *mcp.CallToolResult {
	code, text := classify(err)
	text = urlCredentials.ReplaceAllString(text, "$1")
	structured := map[string]any{"error_code": code, "message": text}
	var te *units.TransitionError
	if errors.As(err, &te) {
		allowed := make([]string, len(te.Allowed))
		for i, st := range te.Allowed {
			allowed[i] = string(st)
		}
		structured["allowed"] = allowed
	}
	var nre *units.NotReadyError
	if errors.As(err, &nre) {
		structured["missing"] = nre.Missing
	}
	var we *units.WaitingError
	if errors.As(err, &we) {
		waiting := make([]map[string]any, len(we.Waiting))
		for i, d := range we.Waiting {
			waiting[i] = map[string]any{"id": d.ID, "title": d.Title, "status": string(d.Status)}
		}
		structured["waiting_on"] = waiting
	}
	var ve *quick.VerificationError
	if errors.As(err, &ve) {
		structured["verification"] = verificationOf(true, ve.Report)
	}
	return &mcp.CallToolResult{
		IsError:           true,
		Content:           []mcp.Content{&mcp.TextContent{Text: text}},
		StructuredContent: structured,
	}
}

func classify(err error) (code, text string) {
	var oe *openError
	switch {
	case errors.As(err, &oe):
		if errors.Is(err, db.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return codeNotInitialized, notInitializedText
		}
	case isDocumentStale(err):
		return codeStale, err.Error() + ". Call doc_get to read the document's current fingerprint and state, then retry."
	case errors.Is(err, documents.ErrInvalid):
		return codeInvalid, err.Error()
	case errors.Is(err, workspace.ErrDirty):
		return codeDirtyTree, dirtyText(err)
	case errors.Is(err, workspace.ErrNotOnBase):
		return codeNotOnBase, err.Error() + ". Ask the user to switch to the base branch, then call quick_start again."
	case errors.Is(err, workspace.ErrBaseAhead):
		return codeBaseAhead, err.Error() + ". Ask the user to push or reset it, then call quick_start again."
	case errors.Is(err, workspace.ErrDetachedHead):
		return codeDetachedHead, err.Error() + ". Ask the user to switch to the base branch, then try again."
	case errors.Is(err, workspace.ErrNoCommits):
		return codeNoCommits, err.Error() + ". Ask the user to make a first commit, then call quick_start again."
	case errors.Is(err, workspace.ErrBranchExists):
		return codeBranchExists, err.Error() + ". Nothing was created. Ask the user to remove or rename that branch, or choose another title, then call quick_start again."
	case errors.Is(err, quick.ErrNotQuickWork):
		return codeNotQuickWork, err.Error() + ". Only a task started with quick_start can be finished with quick_finish; check the id or omit it."
	case errors.Is(err, quick.ErrAmbiguousTask):
		return codeAmbiguousTask, err.Error() + "."
	case errors.Is(err, quick.ErrWrongBranch):
		return codeWrongBranch, err.Error() + ", then call quick_finish again."
	case errors.Is(err, quick.ErrNothingToDeliver):
		return codeNothingToDeliver, err.Error() + ". Make the changes first, then call quick_finish again."
	case errors.Is(err, quick.ErrVerificationFailed):
		return codeVerificationFailed, verificationText(err)
	case errors.Is(err, quick.ErrCommitFailed):
		return codeCommitFailed, err.Error() + ". Fix the cause and call quick_finish again."
	case errors.Is(err, quick.ErrPushFailed):
		return codePushFailed, err.Error() + ". The commit was made. Fix the cause (ask the user if it is access to the remote) and call quick_finish again."
	case errors.Is(err, quick.ErrPullRequestFailed):
		return codePullRequestFailed, pullRequestText(err)
	case errors.Is(err, units.ErrNotFound):
		return codeNotFound, err.Error()
	case errors.Is(err, units.ErrInvalidParent):
		return codeInvalidParent, err.Error()
	case errors.Is(err, units.ErrInvalid):
		return codeInvalid, err.Error()
	case errors.Is(err, units.ErrInvalidTransition):
		return codeTransition, err.Error()
	case errors.Is(err, units.ErrNotReady):
		return codeNotReady, err.Error() + ". Fill in what is missing with unit_update (using the task's latest fingerprint from unit_get), then retry."
	case errors.Is(err, units.ErrReasonRequired):
		return codeReasonRequired, err.Error() + ". Call task_set_state again with a reason."
	case errors.Is(err, units.ErrWaiting):
		return codeWaiting, err.Error() + ". Finishing those slices, or removing the dependency with dependency_remove, lets the task start."
	case errors.Is(err, db.ErrStale):
		return codeStale, err.Error() + ". Call unit_get to read the unit's current fingerprint and state, then retry."
	}
	return codeInternal, "internal error: " + err.Error()
}

const maxListedPaths = 10

func dirtyText(err error) string {
	var de *workspace.DirtyError
	if !errors.As(err, &de) {
		return err.Error() + ". Ask the user to commit or stash their changes, then call quick_start again."
	}
	shown := de.Paths
	if len(shown) > maxListedPaths {
		shown = shown[:maxListedPaths]
	}
	text := "the working tree has uncommitted changes: " + strings.Join(shown, ", ")
	if more := len(de.Paths) - len(shown); more > 0 {
		text += fmt.Sprintf(" and %d more", more)
	}
	return text + ". Nothing was created. Ask the user to commit or stash them, then call quick_start again."
}

const maxOutputTail = 2000

func verificationText(err error) string {
	var b strings.Builder
	b.WriteString(err.Error())
	var ve *quick.VerificationError
	if errors.As(err, &ve) {
		if res, ok := ve.Report.Failed(); ok && res.Output != "" {
			out := strings.TrimRight(res.Output, "\n")
			if len(out) > maxOutputTail {
				out = out[len(out)-maxOutputTail:]
				if i := strings.IndexByte(out, '\n'); i >= 0 {
					out = out[i+1:]
				}
			}
			b.WriteString("\nEnd of its output:\n" + out)
		}
	}
	b.WriteString("\nNothing was committed or pushed. Fix the problem and call quick_finish again.")
	return b.String()
}

func pullRequestText(err error) string {
	text := err.Error() + ". The commit and the push succeeded; call quick_finish again to retry opening the pull request."
	if errors.Is(err, hosting.ErrUnauthorized) {
		if setting, ok := hosting.CredentialSetting(err); ok {
			text += " The host refused the credentials: ask the user to check the setting " + setting + " holds a token that may open pull requests."
		} else {
			text += " The provider rejected the credentials: ask the user to check they may open pull requests."
		}
	}
	return text
}
