package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/bengearig/ralph/internal/workspace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeQuick struct {
	startIn   []quick.StartInput
	finishIn  []quick.FinishInput
	ctxActor  string
	started   quick.Started
	finished  quick.Finished
	startErr  error
	finishErr error
}

func (f *fakeQuick) Start(ctx context.Context, in quick.StartInput) (quick.Started, error) {
	f.startIn = append(f.startIn, in)
	f.ctxActor = units.ActorFrom(ctx)
	return f.started, f.startErr
}

func (f *fakeQuick) Finish(ctx context.Context, in quick.FinishInput) (quick.Finished, error) {
	f.finishIn = append(f.finishIn, in)
	f.ctxActor = units.ActorFrom(ctx)
	return f.finished, f.finishErr
}

func cannedStarted() quick.Started {
	return quick.Started{
		Task: units.Unit{ID: "T-7", Title: "Fix it", Body: "the body", AcceptanceCriteria: "it works",
			State: units.StateInProgress, Fingerprint: "fp1"},
		Branch: "ralph/t-7-fix-it", Base: "main", StartedFrom: "0123456789abcdef",
		Verify: verify.Settings{Commands: []string{"make test", "make lint"}, Timeout: 90 * time.Second, Source: "project (.ralph/config.yaml)"},
	}
}

func cannedFinished() quick.Finished {
	return quick.Finished{
		Task:   units.Unit{ID: "T-7", State: units.StateDone, Fingerprint: "fp2"},
		Branch: "ralph/t-7-fix-it", Base: "develop", Commit: "fedcba9876543210", Committed: true,
		PullRequest:            hosting.PullRequest{Number: 12, URL: "https://example.com/o/r/pull/12", Draft: true, State: hosting.StateOpen},
		Verification:           verify.Report{Results: []verify.Result{{Command: "make test", Duration: 1500 * time.Millisecond}}},
		VerificationConfigured: true,
	}
}

func quickClient(t *testing.T, f *fakeQuick) *mcp.ClientSession {
	t.Helper()
	svc := newSvc(t)
	return connect(t, Options{
		Name: "ralph", Actor: actor, Open: staticOpen(svc),
		Quick: func(context.Context, units.Service) (Quick, error) { return f, nil },
	})
}

func TestQuickToolsListed(t *testing.T) {
	cs := quickClient(t, &fakeQuick{})
	all := tools(t, cs)
	for _, name := range []string{"quick_start", "quick_finish"} {
		tool, found := all[name]
		if !found {
			t.Fatalf("%s not listed", name)
		}
		if tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is read-only", name)
		}
	}
	if got := schemaOf(t, all["quick_start"]).Required; len(got) != 1 || got[0] != "title" {
		t.Errorf("quick_start required = %v, want [title]", got)
	}
	if got := schemaOf(t, all["quick_finish"]).Required; len(got) != 0 {
		t.Errorf("quick_finish required = %v, want none", got)
	}
}

func TestQuickToolsAbsentWithoutOption(t *testing.T) {
	cs, _ := newClient(t)
	all := tools(t, cs)
	for _, name := range []string{"quick_start", "quick_finish"} {
		if _, found := all[name]; found {
			t.Errorf("%s listed without Options.Quick", name)
		}
	}
}

func TestQuickDescriptionsAreActionable(t *testing.T) {
	all := tools(t, quickClient(t, &fakeQuick{}))
	start := all["quick_start"].Description
	for _, want := range []string{"base branch", "not clean", "does not run", "verification commands"} {
		if !strings.Contains(start, want) {
			t.Errorf("quick_start description lacks %q", want)
		}
	}
	finish := all["quick_finish"].Description
	for _, want := range []string{"verification", "pull request", "never merges", "again"} {
		if !strings.Contains(finish, want) {
			t.Errorf("quick_finish description lacks %q", want)
		}
	}
}

func TestQuickStartResult(t *testing.T) {
	f := &fakeQuick{started: cannedStarted()}
	cs := quickClient(t, f)
	res := call(t, cs, "quick_start", map[string]any{"title": "Fix it", "body": "the body", "acceptance_criteria": "it works"})
	if res.IsError {
		t.Fatalf("error result: %s", text(res))
	}
	if len(f.startIn) != 1 || f.startIn[0] != (quick.StartInput{Title: "Fix it", Body: "the body", AcceptanceCriteria: "it works"}) {
		t.Fatalf("fake saw %+v", f.startIn)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var got quickStartResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := quickStartResult{
		Task:   quickTask{ID: "T-7", Title: "Fix it", Body: "the body", AcceptanceCriteria: "it works", State: "IN_PROGRESS", Fingerprint: "fp1"},
		Branch: "ralph/t-7-fix-it", Base: "main", StartedFrom: "0123456789abcdef",
		Verification: quickSettings{Commands: []string{"make test", "make lint"}, Timeout: "1m30s", Source: "project (.ralph/config.yaml)"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("structured = %+v, want %+v", got, want)
	}
	txt := text(res)
	for _, s := range []string{"T-7", "ralph/t-7-fix-it", "make test", "make lint"} {
		if !strings.Contains(txt, s) {
			t.Errorf("text lacks %q: %s", s, txt)
		}
	}
	if !strings.HasSuffix(txt, "then call quick_finish.") {
		t.Errorf("text does not end with the next step: %s", txt)
	}
}

func TestQuickStartMissingTitleRejectedBySchema(t *testing.T) {
	f := &fakeQuick{}
	cs := quickClient(t, f)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "quick_start", Arguments: map[string]any{"body": "x"}})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("missing title accepted: %+v", res)
	}
	if len(f.startIn) != 0 {
		t.Errorf("fake called: %+v", f.startIn)
	}
}

func TestQuickFinishResult(t *testing.T) {
	f := &fakeQuick{finished: cannedFinished()}
	cs := quickClient(t, f)
	res := call(t, cs, "quick_finish", nil)
	if res.IsError {
		t.Fatalf("error result: %s", text(res))
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var got quickFinishResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := quickFinishResult{
		Task:   quickFinishedTask{ID: "T-7", State: "DONE", Fingerprint: "fp2"},
		Branch: "ralph/t-7-fix-it", Base: "develop", Commit: "fedcba9876543210", Committed: true,
		PullRequest:  quickPullRequest{Number: 12, URL: "https://example.com/o/r/pull/12", Draft: true, State: "open"},
		Verification: quickVerification{Configured: true, Results: []quickCheck{{Command: "make test", Passed: true, DurationMS: 1500}}},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("structured = %+v, want %+v", got, want)
	}
	m := res.StructuredContent.(map[string]any)
	for _, k := range []string{"task", "branch", "base", "commit", "committed", "pull_request", "pull_request_reused", "verification"} {
		if _, found := m[k]; !found {
			t.Errorf("structured content lacks %q", k)
		}
	}
	txt := text(res)
	for _, s := range []string{"https://example.com/o/r/pull/12", "git switch develop", "Nothing has been merged", "reviews"} {
		if !strings.Contains(txt, s) {
			t.Errorf("text lacks %q: %s", s, txt)
		}
	}
}

func TestQuickFinishPassesIDAndSummary(t *testing.T) {
	f := &fakeQuick{finished: cannedFinished()}
	cs := quickClient(t, f)
	ok(t, cs, "quick_finish", map[string]any{"id": "T-7", "summary": "did things"})
	if len(f.finishIn) != 1 || f.finishIn[0] != (quick.FinishInput{ID: "T-7", Summary: "did things"}) {
		t.Errorf("fake saw %+v", f.finishIn)
	}
}

func TestQuickFactoryLazyAndCached(t *testing.T) {
	f := &fakeQuick{started: cannedStarted(), finished: cannedFinished()}
	var calls atomic.Int32
	fail := true
	cs := connect(t, Options{
		Name: "ralph", Actor: actor, Open: staticOpen(newSvc(t)),
		Quick: func(context.Context, units.Service) (Quick, error) {
			calls.Add(1)
			if fail {
				return nil, errors.New("boom")
			}
			return f, nil
		},
	})
	ok(t, cs, "status", nil)
	tools(t, cs)
	if calls.Load() != 0 {
		t.Fatalf("factory called %d times before a quick call", calls.Load())
	}
	fails(t, cs, "quick_finish", nil, "internal")
	fail = false
	ok(t, cs, "quick_finish", nil)
	ok(t, cs, "quick_start", map[string]any{"title": "x"})
	if calls.Load() != 2 {
		t.Errorf("factory called %d times, want 2 (one failure, one success)", calls.Load())
	}
}

func TestQuickFactoryNotInitialized(t *testing.T) {
	cs := connect(t, Options{
		Name: "ralph", Actor: actor, Open: staticOpen(newSvc(t)),
		Quick: func(context.Context, units.Service) (Quick, error) {
			return nil, fmt.Errorf("opening: %w", db.ErrNotFound)
		},
	})
	res := fails(t, cs, "quick_start", map[string]any{"title": "x"}, "not_initialized")
	if !strings.Contains(text(res), "ralph init") {
		t.Errorf("text = %q", text(res))
	}
}

func TestQuickErrorCodes(t *testing.T) {
	failing := verify.Report{Results: []verify.Result{{Command: "make test", ExitCode: 2, Output: "boom"}}}
	cases := []struct {
		code   string
		tool   string
		err    error
		expect string
	}{
		{"dirty_tree", "quick_start", &workspace.DirtyError{Paths: []string{"a.go"}}, "a.go"},
		{"not_on_base", "quick_start", &workspace.NotOnBaseError{Current: "x", Base: "main"}, "main"},
		{"base_ahead", "quick_start", &workspace.BaseAheadError{Remote: "origin", Base: "main", Count: 1}, "push"},
		{"detached_head", "quick_start", workspace.ErrDetachedHead, ""},
		{"no_commits", "quick_start", workspace.ErrNoCommits, ""},
		{"branch_exists", "quick_start", fmt.Errorf("%w: b", workspace.ErrBranchExists), "b"},
		{"invalid", "quick_start", fmt.Errorf("%w: the title is empty", units.ErrInvalid), "title"},
		{"not_quick_work", "quick_finish", fmt.Errorf("%w: T-1", quick.ErrNotQuickWork), "T-1"},
		{"ambiguous_task", "quick_finish", fmt.Errorf("%w: T-1, T-2", quick.ErrAmbiguousTask), "T-2"},
		{"wrong_branch", "quick_finish", fmt.Errorf("%w: run git switch b", quick.ErrWrongBranch), "git switch b"},
		{"nothing_to_deliver", "quick_finish", fmt.Errorf("%w: b", quick.ErrNothingToDeliver), ""},
		{"verification_failed", "quick_finish", &quick.VerificationError{Report: failing}, "make test"},
		{"commit_failed", "quick_finish", fmt.Errorf("%w: %w", quick.ErrCommitFailed, &workspace.GitError{Args: []string{"commit"}, ExitCode: 1, Output: "hook said no"}), "hook said no"},
		{"push_failed", "quick_finish", fmt.Errorf("%w: %w", quick.ErrPushFailed, &workspace.GitError{Args: []string{"push"}, ExitCode: 1, Output: "rejected"}), "rejected"},
		{"pull_request_failed", "quick_finish", fmt.Errorf("%w: %w", quick.ErrPullRequestFailed, hosting.ErrUnavailable), "commit and the push succeeded"},
		{"not_found", "quick_finish", fmt.Errorf("get: %w", units.ErrNotFound), ""},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			cs := quickClient(t, &fakeQuick{startErr: c.err, finishErr: c.err})
			args := map[string]any{}
			if c.tool == "quick_start" {
				args["title"] = "x"
			}
			res := fails(t, cs, c.tool, args, c.code)
			if !strings.Contains(text(res), c.expect) {
				t.Errorf("text %q lacks %q", text(res), c.expect)
			}
		})
	}
}

func TestQuickVerificationFailedCarriesOutput(t *testing.T) {
	report := verify.Report{Results: []verify.Result{
		{Command: "make fmt"},
		{Command: "make test", ExitCode: 3, Output: "line one\nFAIL: TestX\n"},
	}}
	cs := quickClient(t, &fakeQuick{finishErr: &quick.VerificationError{Report: report}})
	res := fails(t, cs, "quick_finish", nil, "verification_failed")
	txt := text(res)
	for _, s := range []string{"make test", "3", "FAIL: TestX", "Nothing was committed or pushed", "call quick_finish again"} {
		if !strings.Contains(txt, s) {
			t.Errorf("text lacks %q: %s", s, txt)
		}
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var sc struct {
		Verification quickVerification `json:"verification"`
	}
	if err := json.Unmarshal(raw, &sc); err != nil {
		t.Fatal(err)
	}
	v := sc.Verification
	if !v.Configured || len(v.Results) != 2 || !v.Results[0].Passed || v.Results[1].Passed || v.Results[1].ExitCode != 3 {
		t.Errorf("verification = %+v", v)
	}
}

func TestQuickDirtyTreeListsPaths(t *testing.T) {
	paths := []string{"a.go", "dir/b.go"}
	cs := quickClient(t, &fakeQuick{startErr: &workspace.DirtyError{Paths: paths}})
	res := fails(t, cs, "quick_start", map[string]any{"title": "x"}, "dirty_tree")
	txt := text(res)
	for _, s := range append(paths, "commit or stash") {
		if !strings.Contains(txt, s) {
			t.Errorf("text lacks %q: %s", s, txt)
		}
	}
}

func TestQuickPullRequestFailedUnauthorizedNamesToken(t *testing.T) {
	err := fmt.Errorf("%w: %w; the branch is pushed", quick.ErrPullRequestFailed, hosting.ErrUnauthorized)
	cs := quickClient(t, &fakeQuick{finishErr: err})
	res := fails(t, cs, "quick_finish", nil, "pull_request_failed")
	if !strings.Contains(text(res), "github.token") {
		t.Errorf("text = %q", text(res))
	}
}

func TestQuickErrorsNeverContainCredentials(t *testing.T) {
	ge := &workspace.GitError{Args: []string{"push"}, ExitCode: 128, Output: "fatal: unable to access 'https://user:secret@host/x'"}
	cs := quickClient(t, &fakeQuick{finishErr: fmt.Errorf("%w: %w", quick.ErrPushFailed, ge)})
	res := fails(t, cs, "quick_finish", nil, "push_failed")
	raw, _ := json.Marshal(res.StructuredContent)
	if all := text(res) + string(raw); strings.Contains(all, "secret") {
		t.Errorf("credential leaked: %s", all)
	}
}

func TestQuickWritesCarryActor(t *testing.T) {
	f := &fakeQuick{started: cannedStarted(), finished: cannedFinished()}
	cs := quickClient(t, f)
	ok(t, cs, "quick_start", map[string]any{"title": "x"})
	if f.ctxActor != actor {
		t.Errorf("start actor = %q, want %q", f.ctxActor, actor)
	}
	f.ctxActor = ""
	ok(t, cs, "quick_finish", nil)
	if f.ctxActor != actor {
		t.Errorf("finish actor = %q, want %q", f.ctxActor, actor)
	}
}
