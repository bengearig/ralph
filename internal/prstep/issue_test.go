package prstep_test

import (
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/prstep"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/units"
)

func (e *env) link(t *testing.T, id, target string) {
	t.Helper()
	if err := e.units.AddLink(ctx, id, units.LinkExternal, target); err != nil {
		t.Fatal(err)
	}
}

func TestTicketKeyFromNearestAncestor(t *testing.T) {
	e := newEnv(t)
	e.link(t, e.brief.ID, "issue:jira:ABC-1")
	got, err := prstep.SliceTicket(ctx, e.units, e.slice)
	if err != nil || got != "ABC-1" {
		t.Fatalf("brief's key: %q, %v", got, err)
	}
	e.link(t, e.slice.ID, "issue:jira:ABC-2")
	got, err = prstep.SliceTicket(ctx, e.units, e.slice)
	if err != nil || got != "ABC-2" {
		t.Fatalf("own key: %q, %v", got, err)
	}
}

func TestBranchKeptForExistingSliceBranch(t *testing.T) {
	e := newEnv(t)
	if err := prstep.RecordBranch(ctx, e.units, e.slice.ID, e.branch); err != nil {
		t.Fatal(err)
	}
	e.link(t, e.brief.ID, "issue:jira:ABC-9")
	got, err := prstep.SliceBranch(ctx, e.units, e.deps.Config.BranchTemplate, e.slice)
	if err != nil || got != e.branch {
		t.Fatalf("SliceBranch = %q, %v; want %q", got, err, e.branch)
	}
	res := e.mustRun(t)
	if res.Branch != e.branch || e.originHead(t) == "" {
		t.Fatalf("branch = %q, want %q", res.Branch, e.branch)
	}
}

func TestPRBodyIssueLineForSlice(t *testing.T) {
	e := newEnv(t)
	if err := prstep.RecordBranch(ctx, e.units, e.slice.ID, e.branch); err != nil {
		t.Fatal(err)
	}
	e.link(t, e.brief.ID, "issue:jira:ABC-9")
	e.deps.Config.JiraSite = "https://example.atlassian.net"
	res := e.mustRun(t)
	pr, ok, err := e.provider.FindPullRequest(ctx, res.Branch)
	if err != nil || !ok {
		t.Fatalf("pr: %v, %v", ok, err)
	}
	if want := "ABC-9 " + e.slice.ID + ": The slice"; pr.Title != want {
		t.Errorf("title = %q, want %q", pr.Title, want)
	}
	want := "Issue: ABC-9 https://example.atlassian.net/browse/ABC-9\n\nOpened by ralph for " + e.slice.ID + ".\n"
	if !strings.HasSuffix(pr.Body, want) {
		t.Errorf("body = %q", pr.Body)
	}
}

func TestBuildBodyIssueLineWithoutSite(t *testing.T) {
	got := prstep.BuildBody(prstep.BodyInput{SliceID: "S-1", Issue: &quick.BodyIssue{Key: "ABC-9"}})
	if !strings.HasSuffix(got, "\n\nIssue: ABC-9\n\nOpened by ralph for S-1.\n") {
		t.Errorf("body = %q", got)
	}
}
