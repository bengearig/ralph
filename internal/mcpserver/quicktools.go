package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Quick is what the quick work tools need; *quick.Service satisfies it.
type Quick interface {
	Start(ctx context.Context, in quick.StartInput) (quick.Started, error)
	Finish(ctx context.Context, in quick.FinishInput) (quick.Finished, error)
}

const quickStartDescription = `Start a small piece of work the user has agreed on. This creates one orphan task (no brief, no slice) in progress and its own branch from the base branch, then returns them. It refuses, creating nothing, when the working tree is not clean or the checkout is not on the base branch; in that case ask the user to commit or stash their changes or to switch branches, then call it again.

title is required, and must be one line, unless issue is given. issue is a ticket key such as ABC-123 or the address of the ticket: ralph reads the ticket before creating anything and links the new task to it, and title and body default to the ticket's title and description; give your own only when you have a better one. A ticket that cannot be read, or that is already linked to a unit, ends the call with an error and nothing created. body says what to do and why; it defaults to the title. acceptance_criteria is prose that you and the reviewer read: ralph does not run it. The verification commands in the result are what quick_finish will run, so make them pass.

After this call, implement the task in the working tree, then call quick_finish.`

const quickFinishDescription = `Deliver quick work started with quick_start. It runs the project's configured verification commands, commits everything in the working tree, pushes the branch and opens a pull request for a person to review. It never merges. With no id it finishes the quick work task on the current branch.

It stops at the first problem and changes nothing before verification passes: fix what the error says and call quick_finish again. It may be called again after any failure and resumes where it stopped: the commit, the pushed branch and the pull request are reused.

summary is a short account of what changed, for the pull request description.`

type quickStartInput struct {
	Title              string `json:"title,omitempty" jsonschema:"short one-line title, not blank; required unless issue is given, then it defaults to the ticket's title"`
	Body               string `json:"body,omitempty" jsonschema:"what to do and why; defaults to the title"`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty" jsonschema:"how the reviewer can tell the work is done; read by people, not run by ralph"`
	Issue              string `json:"issue,omitempty" jsonschema:"a ticket key such as ABC-123 or the address of the ticket to start from; when given, title and body may be omitted and default to the ticket's"`
}

type quickFinishInput struct {
	ID      string `json:"id,omitempty" jsonschema:"the quick work task ID such as T-5; omit to use the task on the current branch"`
	Summary string `json:"summary,omitempty" jsonschema:"short account of what changed, for the pull request description"`
}

type QuickTask struct {
	ID                 string `json:"id"`
	Title              string `json:"title"`
	Body               string `json:"body"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
	State              string `json:"state"`
	Fingerprint        string `json:"fingerprint"`
}

type QuickSettings struct {
	Commands []string `json:"commands"`
	Timeout  string   `json:"timeout"`
	Source   string   `json:"source"`
}

// QuickIssue is the ticket quick work was started from.
type QuickIssue struct {
	Key    string `json:"key"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

type QuickStartResult struct {
	Task         QuickTask     `json:"task"`
	Branch       string        `json:"branch"`
	Base         string        `json:"base"`
	StartedFrom  string        `json:"started_from"`
	Verification QuickSettings `json:"verification"`
	Issue        *QuickIssue   `json:"issue,omitempty"`
}

type QuickFinishedTask struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	Fingerprint string `json:"fingerprint"`
}

type QuickPullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Draft  bool   `json:"draft"`
	State  string `json:"state"`
}

type QuickCheck struct {
	Command    string `json:"command"`
	Passed     bool   `json:"passed"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
}

type QuickVerification struct {
	Configured bool         `json:"configured"`
	Results    []QuickCheck `json:"results"`
}

type QuickFinishResult struct {
	Task              QuickFinishedTask `json:"task"`
	Branch            string            `json:"branch"`
	Base              string            `json:"base"`
	Commit            string            `json:"commit"`
	Committed         bool              `json:"committed"`
	PullRequest       QuickPullRequest  `json:"pull_request"`
	PullRequestReused bool              `json:"pull_request_reused"`
	Verification      QuickVerification `json:"verification"`
}

func verificationOf(configured bool, report verify.Report) QuickVerification {
	out := QuickVerification{Configured: configured, Results: make([]QuickCheck, len(report.Results))}
	for i, r := range report.Results {
		out.Results[i] = QuickCheck{
			Command: r.Command, Passed: r.Passed(), ExitCode: r.ExitCode,
			DurationMS: r.Duration.Milliseconds(), TimedOut: r.TimedOut,
		}
	}
	return out
}

// NewQuickStartResult builds the quick_start result from what the service returned.
func NewQuickStartResult(st quick.Started) QuickStartResult {
	res := QuickStartResult{
		Task: QuickTask{
			ID: st.Task.ID, Title: st.Task.Title, Body: st.Task.Body,
			AcceptanceCriteria: st.Task.AcceptanceCriteria, State: string(st.Task.State),
			Fingerprint: st.Task.Fingerprint,
		},
		Branch: st.Branch, Base: st.Base, StartedFrom: st.StartedFrom,
		Verification: QuickSettings{
			Commands: append([]string{}, st.Verify.Commands...),
			Timeout:  st.Verify.Timeout.String(), Source: st.Verify.Source,
		},
	}
	if st.Issue != nil {
		i := QuickIssue(*st.Issue)
		res.Issue = &i
	}
	return res
}

// NewQuickFinishResult builds the quick_finish result from what the service returned.
func NewQuickFinishResult(fin quick.Finished) QuickFinishResult {
	return QuickFinishResult{
		Task:   QuickFinishedTask{ID: fin.Task.ID, State: string(fin.Task.State), Fingerprint: fin.Task.Fingerprint},
		Branch: fin.Branch, Base: fin.Base, Commit: fin.Commit, Committed: fin.Committed,
		PullRequest: QuickPullRequest{
			Number: fin.PullRequest.Number, URL: fin.PullRequest.URL,
			Draft: fin.PullRequest.Draft, State: string(fin.PullRequest.State),
		},
		PullRequestReused: fin.PullRequestReused,
		Verification:      verificationOf(fin.VerificationConfigured, fin.Verification),
	}
}

// quickService returns the kept quick work service, building it on the first
// call or when every earlier attempt failed.
func (s *server) quickService(ctx context.Context, svc units.Service) (Quick, error) {
	s.quickMu.Lock()
	defer s.quickMu.Unlock()
	if s.quick != nil {
		return s.quick, nil
	}
	q, err := s.opts.Quick(ctx, svc)
	if err != nil {
		return nil, &openError{err}
	}
	s.quick = q
	return q, nil
}

func (s *server) addQuickTools(srv *mcp.Server) {
	if s.opts.Quick == nil {
		return
	}
	no := false
	writes := &mcp.ToolAnnotations{DestructiveHint: &no}

	addTool(s, srv, &mcp.Tool{
		Name:         "quick_start",
		Description:  quickStartDescription,
		Annotations:  writes,
		InputSchema:  schemaFor[quickStartInput](nil),
		OutputSchema: outputFor[QuickStartResult](),
	}, func(ctx context.Context, svc units.Service, in quickStartInput) (any, string, error) {
		q, err := s.quickService(ctx, svc)
		if err != nil {
			return nil, "", err
		}
		st, err := q.Start(ctx, quick.StartInput{Title: in.Title, Body: in.Body, AcceptanceCriteria: in.AcceptanceCriteria, Issue: in.Issue})
		if err != nil {
			return nil, "", err
		}
		out := NewQuickStartResult(st)
		return out, startText(out), nil
	})

	addTool(s, srv, &mcp.Tool{
		Name:         "quick_finish",
		Description:  quickFinishDescription,
		Annotations:  writes,
		InputSchema:  schemaFor[quickFinishInput](nil),
		OutputSchema: outputFor[QuickFinishResult](),
	}, func(ctx context.Context, svc units.Service, in quickFinishInput) (any, string, error) {
		q, err := s.quickService(ctx, svc)
		if err != nil {
			return nil, "", err
		}
		fin, err := q.Finish(ctx, quick.FinishInput{ID: in.ID, Summary: in.Summary})
		if err != nil {
			return nil, "", err
		}
		out := NewQuickFinishResult(fin)
		return out, finishText(out), nil
	})
}

func startText(r QuickStartResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Started %s %q on branch %s, created from %s at %s.\n", r.Task.ID, r.Task.Title, r.Branch, r.Base, shortHash(r.StartedFrom))
	if len(r.Verification.Commands) == 0 {
		b.WriteString("No verification commands are configured, so quick_finish will not verify the work.\n")
	} else {
		fmt.Fprintf(&b, "quick_finish will run these verification commands (timeout %s each):\n", r.Verification.Timeout)
		for _, c := range r.Verification.Commands {
			fmt.Fprintf(&b, "  %s\n", c)
		}
	}
	if r.Issue != nil {
		fmt.Fprintf(&b, "Started from %s %q (%s): %s\n", r.Issue.Key, r.Issue.Title, r.Issue.Status, r.Issue.URL)
	}
	b.WriteString("Implement the task in the working tree, then call quick_finish.")
	return b.String()
}

func finishText(r QuickFinishResult) string {
	var b strings.Builder
	switch {
	case r.PullRequestReused:
		fmt.Fprintf(&b, "Pull request #%d for %s already existed: %s\n", r.PullRequest.Number, r.Branch, r.PullRequest.URL)
	default:
		fmt.Fprintf(&b, "Opened pull request #%d for %s: %s\n", r.PullRequest.Number, r.Branch, r.PullRequest.URL)
	}
	if r.Committed {
		fmt.Fprintf(&b, "Committed %s and pushed %s.\n", shortHash(r.Commit), r.Branch)
	} else {
		fmt.Fprintf(&b, "Nothing new to commit; pushed %s at %s.\n", r.Branch, shortHash(r.Commit))
	}
	if r.Verification.Configured {
		fmt.Fprintf(&b, "Verification passed (%d command(s)).\n", len(r.Verification.Results))
	} else {
		b.WriteString("No verification commands are configured, so the work was not verified.\n")
	}
	fmt.Fprintf(&b, "Nothing has been merged: a person reviews the pull request. To return to the base branch, run `git switch %s`.", r.Base)
	return b.String()
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}
