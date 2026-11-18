package quick

import (
	"context"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/units"
)

const (
	ticketBranchTemplate  = "ralph/{{if .Ticket}}{{lower .Ticket}}-{{end}}{{lower .ID}}-{{.Slug}}"
	ticketSubjectTemplate = "{{if .Ticket}}{{.Ticket}} {{end}}{{.ID}}: {{.Title}}"
)

func TestStartIssueBranchCarriesKey(t *testing.T) {
	f := newFixture(t)
	f.cfg.BranchTemplate = ticketBranchTemplate
	f.withIssue(t, ticket("ABC-12", "Fix the login", "body"))
	got, err := f.startIssue(StartInput{Issue: "ABC-12"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "ralph/abc-12-t-1-fix-the-login" {
		t.Fatalf("branch = %q", got.Branch)
	}
}

func TestFinishIssueSubjectCarriesKey(t *testing.T) {
	f := newFinishFixture(t)
	f.cfg.BranchTemplate = ticketBranchTemplate
	f.cfg.CommitTemplate = ticketSubjectTemplate
	f.cfg.JiraSite = "https://example.atlassian.net/"
	f.withIssue(t, ticket("ABC-12", "Fix the login", "body"))
	st, err := f.startIssue(StartInput{Issue: "ABC-12"})
	if err != nil {
		t.Fatal(err)
	}
	f.repo.WriteFile("new.txt", "hello\n")
	if _, err := f.finish(FinishInput{ID: st.Task.ID}); err != nil {
		t.Fatal(err)
	}
	const subject = "ABC-12 T-1: Fix the login"
	if msg := f.repo.Git("log", "-1", "--format=%s"); msg != subject {
		t.Fatalf("commit subject = %q", msg)
	}
	pr, ok, _ := f.prov.FindPullRequest(context.Background(), st.Branch)
	if !ok || pr.Title != subject {
		t.Fatalf("pr = %+v", pr)
	}
	want := "Issue: ABC-12 https://example.atlassian.net/browse/ABC-12\n\nOpened by ralph for T-1.\n"
	if !strings.HasSuffix(pr.Body, want) {
		t.Fatalf("body = %q", pr.Body)
	}
}

func TestBranchKeptWhenIssueLinkAddedLater(t *testing.T) {
	f := newFinishFixture(t)
	f.cfg.BranchTemplate = ticketBranchTemplate
	f.cfg.CommitTemplate = ticketSubjectTemplate
	st := f.started(t, "Add a file")
	if st.Branch != "ralph/t-1-add-a-file" {
		t.Fatalf("branch = %q", st.Branch)
	}
	if err := f.units.AddLink(context.Background(), st.Task.ID, units.LinkExternal, "issue:jira:ABC-12"); err != nil {
		t.Fatal(err)
	}
	got, err := f.finish(FinishInput{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != st.Branch || !f.remoteHas(st.Branch) {
		t.Fatalf("branch = %q, want %q", got.Branch, st.Branch)
	}
	if msg := f.repo.Git("log", "-1", "--format=%s"); msg != "ABC-12 T-1: Add a file" {
		t.Fatalf("subject = %q", msg)
	}
}

func TestBuildBodyIssueLineWithUrl(t *testing.T) {
	got := BuildPullRequestBody(BodyInput{ID: "T-1", Body: "b", Configured: true,
		Issue: &BodyIssue{Key: "ABC-1", URL: IssueURL("https://x.atlassian.net/", "ABC-1")}})
	want := "b\n\nIssue: ABC-1 https://x.atlassian.net/browse/ABC-1\n\nOpened by ralph for T-1.\n"
	if got != want {
		t.Fatalf("body = %q", got)
	}
}

func TestBuildBodyIssueLineWithoutSite(t *testing.T) {
	got := BuildPullRequestBody(BodyInput{ID: "T-1", Configured: true,
		Issue: &BodyIssue{Key: "ABC-1", URL: IssueURL("", "ABC-1")}})
	if got != "Issue: ABC-1\n\nOpened by ralph for T-1.\n" {
		t.Fatalf("body = %q", got)
	}
}

func TestBuildBodyNoIssueUnchanged(t *testing.T) {
	got := BuildPullRequestBody(BodyInput{ID: "T-2", Configured: true})
	if got != "Opened by ralph for T-2.\n" {
		t.Fatalf("body = %q", got)
	}
}
