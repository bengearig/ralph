package quick

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/issuelink"
	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/issuesource/issuetest"
	"github.com/bengearig/ralph/internal/tickets"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/workspace"
)

const issueHost = "example.atlassian.net"

func (f *fixture) withIssue(t *testing.T, i issuesource.Issue) *issuetest.Fake {
	t.Helper()
	fake := issuetest.NewFake("jira", issueHost)
	fake.Add(i)
	f.issues = fake
	return fake
}

func ticket(key, title, body string) issuesource.Issue {
	return issuesource.Issue{
		Ref: issuesource.Ref{System: "jira", Key: key}, Title: title, Body: body, Type: "Bug", Status: "To Do",
	}
}

func (f *fixture) startIssue(in StartInput) (Started, error) {
	return f.service().Start(context.Background(), in)
}

func (f *fixture) noBranch(t *testing.T) {
	t.Helper()
	if cur := f.repo.Git("branch", "--show-current"); cur != "main" {
		t.Fatalf("checkout on %q, want main", cur)
	}
	if branches := f.repo.Git("branch", "--list"); strings.Count(branches, "\n") > 0 {
		t.Fatalf("branches = %q", branches)
	}
}

func TestStartIssueDefaultsTitleAndBodyFromTicket(t *testing.T) {
	f := newFixture(t)
	f.withIssue(t, ticket("ABC-12", "Fix the login", "The login page crashes."))
	got, err := f.startIssue(StartInput{Issue: "abc-12", AcceptanceCriteria: "it does not crash"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Task.Title != "Fix the login" || got.Task.Body != "The login page crashes." || got.Task.AcceptanceCriteria != "it does not crash" {
		t.Fatalf("task = %+v", got.Task)
	}
	if got.Task.State != units.StateInProgress {
		t.Fatalf("state = %s", got.Task.State)
	}
}

func TestStartIssueGivenTitleWins(t *testing.T) {
	f := newFixture(t)
	f.withIssue(t, ticket("ABC-12", "Fix the login", "The login page crashes."))
	got, err := f.startIssue(StartInput{Issue: "ABC-12", Title: "My title", Body: "My body"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Task.Title != "My title" || got.Task.Body != "My body" {
		t.Fatalf("task = %+v", got.Task)
	}
}

func TestStartIssueLongTitleCutAtWordBoundary(t *testing.T) {
	f := newFixture(t)
	long := strings.Repeat("word ", 60)
	f.withIssue(t, ticket("ABC-12", long, "body"))
	got, err := f.startIssue(StartInput{Issue: "ABC-12"})
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSpace(strings.Repeat("word ", 40)); got.Task.Title != want {
		t.Fatalf("title = %q (%d runes)", got.Task.Title, len([]rune(got.Task.Title)))
	}

	f2 := newFixture(t)
	f2.withIssue(t, ticket("ABC-13", strings.Repeat("x", 250), "body"))
	got, err = f2.startIssue(StartInput{Issue: "ABC-13"})
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(got.Task.Title)) != maxTitle {
		t.Fatalf("a title with no space is cut to %d runes, got %d", maxTitle, len([]rune(got.Task.Title)))
	}
}

func TestStartIssueEmptyDescriptionUsesTitleAsBody(t *testing.T) {
	f := newFixture(t)
	f.withIssue(t, ticket("ABC-12", "Fix the login", "  \n"))
	got, err := f.startIssue(StartInput{Issue: "ABC-12"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Task.Body != "Fix the login" {
		t.Fatalf("body = %q", got.Task.Body)
	}
}

func TestStartIssueWritesBothLinksAndRow(t *testing.T) {
	f := newFixture(t)
	f.withIssue(t, ticket("ABC-12", "Fix the login", "body"))
	got, err := f.startIssue(StartInput{Issue: "ABC-12"})
	if err != nil {
		t.Fatal(err)
	}
	links, err := f.units.Links(context.Background(), got.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	for _, l := range links {
		targets = append(targets, l.Target)
	}
	for _, want := range []string{"issue:jira:ABC-12", "jira:ABC-12", "branch:" + got.Branch} {
		if !slices.Contains(targets, want) {
			t.Errorf("links %v lack %q", targets, want)
		}
	}
	row, ok, err := tickets.NewStore(f.db).ByKey(context.Background(), "jira", "ABC-12")
	if err != nil || !ok {
		t.Fatalf("row: %v, %v", ok, err)
	}
	if row.UnitID != got.Task.ID || row.Origin != tickets.OriginIssue {
		t.Fatalf("row = %+v", row)
	}
}

func TestStartIssueNoSourceConfigured(t *testing.T) {
	f := newFixture(t)
	_, err := f.startIssue(StartInput{Issue: "ABC-12"})
	if err == nil || err.Error() != "no issue source is configured: set issues.provider to jira" {
		t.Fatalf("err = %v", err)
	}
	f.noUnits(t)
	f.noBranch(t)
}

func TestStartIssueInvalidRef(t *testing.T) {
	f := newFixture(t)
	f.withIssue(t, ticket("ABC-12", "Fix", "body"))
	_, err := f.startIssue(StartInput{Issue: "not a ticket"})
	if !errors.Is(err, issuesource.ErrInvalid) || !strings.Contains(err.Error(), "use a key such as ABC-123") {
		t.Fatalf("err = %v", err)
	}
	f.noUnits(t)
	f.noBranch(t)
}

func TestStartIssueNotFoundCreatesNothing(t *testing.T) {
	f := newFixture(t)
	f.withIssue(t, ticket("ABC-12", "Fix", "body"))
	_, err := f.startIssue(StartInput{Issue: "ABC-99"})
	if !errors.Is(err, issuesource.ErrNotFound) || !strings.Contains(err.Error(), "ABC-99") {
		t.Fatalf("err = %v", err)
	}
	f.noUnits(t)
	f.noBranch(t)
}

func TestStartIssueUnavailableCreatesNothing(t *testing.T) {
	for name, cause := range map[string]error{
		"unavailable":  issuesource.ErrUnavailable,
		"unauthorized": issuesource.ErrUnauthorized,
		"rate limited": issuesource.ErrRateLimited,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			fake := f.withIssue(t, ticket("ABC-12", "Fix", "body"))
			fake.FailWith(cause)
			_, err := f.startIssue(StartInput{Issue: "ABC-12"})
			if !errors.Is(err, cause) || !strings.Contains(err.Error(), "ABC-12") {
				t.Fatalf("err = %v", err)
			}
			f.noUnits(t)
			f.noBranch(t)
		})
	}
}

func TestStartIssueWorkspaceRefusalsComeFirst(t *testing.T) {
	f := newFixture(t)
	fake := f.withIssue(t, ticket("ABC-12", "Fix", "body"))
	f.repo.WriteFile("stray.txt", "x")
	_, err := f.startIssue(StartInput{Issue: "ABC-12"})
	if !errors.Is(err, workspace.ErrDirty) {
		t.Fatalf("err = %v", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Fatalf("the source was called: %v", calls)
	}
	f.noUnits(t)
}

func TestStartIssueTicketAlreadyLinkedRefused(t *testing.T) {
	f := newFixture(t)
	f.withIssue(t, ticket("ABC-12", "Fix", "body"))
	first, err := f.startIssue(StartInput{Issue: "ABC-12"})
	if err != nil {
		t.Fatal(err)
	}
	f.repo.Git("switch", "main")
	before, err := f.units.List(context.Background(), units.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.startIssue(StartInput{Issue: "ABC-12"})
	if !errors.Is(err, issuelink.ErrAlreadyLinked) || err.Error() != "ABC-12 is already linked to "+first.Task.ID {
		t.Fatalf("err = %v", err)
	}
	after, err := f.units.List(context.Background(), units.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("units %d -> %d", len(before), len(after))
	}
	if branches := f.repo.Git("branch", "--list"); strings.Count(branches, "\n") != 1 {
		t.Fatalf("branches = %q", branches)
	}
}

type failIssueLink struct{ units.Service }

func (s failIssueLink) AddLink(ctx context.Context, id string, kind units.LinkKind, target string) error {
	if strings.HasPrefix(target, "issue:") {
		return errors.New("link store is down")
	}
	return s.Service.AddLink(ctx, id, kind, target)
}

func TestStartIssueAttachFailureCancelsTask(t *testing.T) {
	f := newFixture(t)
	f.withIssue(t, ticket("ABC-12", "Fix", "body"))
	f.units = failIssueLink{f.units}
	_, err := f.startIssue(StartInput{Issue: "ABC-12"})
	if err == nil || !strings.Contains(err.Error(), "link store is down") {
		t.Fatalf("err = %v", err)
	}
	if u := f.get(t, "T-1"); u.State != units.StateCancelled {
		t.Fatalf("state = %s", u.State)
	}
	f.noBranch(t)
}

func TestStartIssueNoteNamesTicket(t *testing.T) {
	f := newFixture(t)
	f.withIssue(t, ticket("ABC-12", "Fix", "body"))
	got, err := f.startIssue(StartInput{Issue: "ABC-12"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range f.notes(t, got.Task.ID) {
		if strings.Contains(n.Body, got.Branch) && strings.Contains(n.Body, "Started from ABC-12") {
			found = true
		}
	}
	if !found {
		t.Fatalf("notes = %+v", f.notes(t, got.Task.ID))
	}
}

func TestStartIssueResultCarriesIssue(t *testing.T) {
	f := newFixture(t)
	i := ticket("ABC-12", "Fix", "body")
	f.withIssue(t, i)
	got, err := f.startIssue(StartInput{Issue: "https://" + issueHost + "/browse/ABC-12"})
	if err != nil {
		t.Fatal(err)
	}
	want := IssueInfo{Key: "ABC-12", URL: "https://" + issueHost + "/browse/ABC-12", Title: "Fix", Type: "Bug", Status: "To Do"}
	if got.Issue == nil || *got.Issue != want {
		t.Fatalf("issue = %+v, want %+v", got.Issue, want)
	}
}

func TestStartWithoutIssueUnchanged(t *testing.T) {
	f := newFixture(t)
	got, err := f.start(t, "Plain work")
	if err != nil {
		t.Fatal(err)
	}
	if got.Issue != nil {
		t.Fatalf("issue = %+v", got.Issue)
	}
	for _, n := range f.notes(t, got.Task.ID) {
		if strings.Contains(n.Body, "Started from") {
			t.Fatalf("note = %q", n.Body)
		}
	}
	if _, err := f.start(t, "  "); !errors.Is(err, units.ErrInvalid) {
		t.Fatalf("blank title: %v", err)
	}
}

func TestStartIssueBranchCarriesTicketKey(t *testing.T) {
	f := newFixture(t)
	f.withIssue(t, ticket("ABC-12", "Fix", "body"))
	f.cfg.BranchTemplate = "ralph/{{if .Ticket}}{{lower .Ticket}}-{{end}}{{lower .ID}}-{{.Slug}}"
	got, err := f.startIssue(StartInput{Issue: "ABC-12"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "ralph/abc-12-t-1-fix" {
		t.Fatalf("branch = %q", got.Branch)
	}
}
