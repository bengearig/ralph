package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/issuelink"
	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

func TestQuickStartIssueInputOptionalTitle(t *testing.T) {
	f := &fakeQuick{started: cannedStarted()}
	cs := quickClient(t, f)
	start := tools(t, cs)["quick_start"]
	props, _ := json.Marshal(start.InputSchema)
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(props, &schema); err != nil {
		t.Fatal(err)
	}
	desc := schema.Properties["issue"].Description
	if !strings.Contains(desc, "ticket key") || !strings.Contains(desc, "title and body may be omitted") {
		t.Errorf("issue description = %q", desc)
	}
	if res := call(t, cs, "quick_start", map[string]any{"issue": "ABC-12"}); res.IsError {
		t.Fatalf("error result: %s", text(res))
	}
	if len(f.startIn) != 1 || f.startIn[0] != (quick.StartInput{Issue: "ABC-12"}) {
		t.Fatalf("fake saw %+v", f.startIn)
	}
}

func TestQuickStartIssueOutput(t *testing.T) {
	f := &fakeQuick{started: cannedStarted()}
	f.started.Issue = &quick.IssueInfo{Key: "ABC-12", URL: "https://x.example/browse/ABC-12", Title: "Fix it", Type: "Bug", Status: "To Do"}
	cs := quickClient(t, f)
	res := call(t, cs, "quick_start", map[string]any{"issue": "ABC-12"})
	if res.IsError {
		t.Fatalf("error result: %s", text(res))
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var got QuickStartResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := QuickIssue{Key: "ABC-12", URL: "https://x.example/browse/ABC-12", Title: "Fix it", Type: "Bug", Status: "To Do"}
	if got.Issue == nil || *got.Issue != want {
		t.Fatalf("issue = %+v, want %+v", got.Issue, want)
	}
	if !strings.Contains(text(res), "ABC-12") {
		t.Errorf("text lacks the ticket: %s", text(res))
	}

	f.started.Issue = nil
	res = call(t, cs, "quick_start", map[string]any{"title": "Fix it"})
	var plain map[string]any
	raw, _ = json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &plain); err != nil {
		t.Fatal(err)
	}
	if _, has := plain["issue"]; has {
		t.Errorf("issue present without a ticket: %v", plain["issue"])
	}
}

func TestQuickStartTitleRequiredWithoutIssue(t *testing.T) {
	testutil.IsolateEnv(t)
	repo := testutil.NewRepo(t)
	svc := newSvc(t)
	ws, err := quick.NewLocalWorkspace(repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	real := quick.New(quick.Deps{
		Units: svc, Workspace: ws,
		Config: quick.Config{Root: repo.Dir, Remote: "origin", BranchTemplate: "ralph/{{.ID | lower}}-{{.Slug}}"},
	})
	cs := connect(t, Options{
		Name: "ralph", Actor: actor, Open: staticOpen(svc),
		Quick: func(context.Context, units.Service) (Quick, error) { return real, nil },
	})
	res := fails(t, cs, "quick_start", map[string]any{"body": "x"}, "invalid")
	if !strings.Contains(text(res), "title") {
		t.Errorf("text = %q", text(res))
	}
	list, err := svc.List(context.Background(), units.Filter{})
	if err != nil || len(list) != 0 {
		t.Fatalf("units = %v, %v", list, err)
	}
}

func TestQuickStartIssueErrorCodes(t *testing.T) {
	cases := []struct {
		code, expect string
		err          error
	}{
		{"not_configured", "issues.provider", quick.ErrNoIssueSource},
		{"not_configured", "jira.site", &NotConfiguredError{Message: "jira.site is not set: set it to use Jira"}},
		{"invalid", "use a key", fmt.Errorf("%w: not a jira ticket: use a key such as ABC-123", issuesource.ErrInvalid)},
		{"not_found", "ABC-12", fmt.Errorf("%w: ABC-12 was not found", issuesource.ErrNotFound)},
		{"unauthorized", "", fmt.Errorf("read ABC-12: %w", issuesource.ErrUnauthorized)},
		{"rate_limited", "ABC-12", fmt.Errorf("read ABC-12: %w", issuesource.ErrRateLimited)},
		{"unavailable", "ABC-12", fmt.Errorf("read ABC-12: %w", issuesource.ErrUnavailable)},
		{"invalid", "already linked", errors.Join(fmt.Errorf("ABC-12 is already linked to T-3: %w", issuelink.ErrAlreadyLinked))},
	}
	for _, c := range cases {
		t.Run(c.code+" "+c.expect, func(t *testing.T) {
			cs := quickClient(t, &fakeQuick{startErr: c.err})
			res := fails(t, cs, "quick_start", map[string]any{"issue": "ABC-12"}, c.code)
			if !strings.Contains(text(res), c.expect) {
				t.Errorf("text %q lacks %q", text(res), c.expect)
			}
		})
	}
}
