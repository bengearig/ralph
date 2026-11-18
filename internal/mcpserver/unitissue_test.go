package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/issuesource/issuetest"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/tickets"
	"github.com/bengearig/ralph/internal/units"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type issueEnv struct {
	svc  units.Service
	db   *db.DB
	fake *issuetest.Fake
	cs   *mcp.ClientSession
}

func newIssueEnv(t *testing.T, wrap func(units.Service) units.Service) *issueEnv {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	svc := units.NewService(d)
	fake := seededFake()
	open := svc
	if wrap != nil {
		open = wrap(svc)
	}
	cs := connect(t, Options{
		Name: "ralph", Actor: actor, Open: staticOpen(open),
		Issues: func(context.Context) (issuesource.Source, error) { return fake, nil },
		Tickets: func(context.Context, units.Service) (tickets.Deps, error) {
			return tickets.Deps{DB: d, Settings: taskmanager.Settings{Provider: "jira"}}, nil
		},
	})
	return &issueEnv{svc: svc, db: d, fake: fake, cs: cs}
}

func (e *issueEnv) count(t *testing.T) int {
	t.Helper()
	list, err := e.svc.List(context.Background(), units.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func briefArgs(issue string) map[string]any {
	return map[string]any{"level": "brief", "title": "Fix the thing", "body": "Jira ABC-1: Fix the thing\n\nIt is broken.", "issue": issue}
}

func TestUnitCreateIssueBrief(t *testing.T) {
	e := newIssueEnv(t, nil)
	out := ok(t, e.cs, "unit_create", briefArgs("abc-1"))
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("no id: %v", out)
	}
	u, err := e.svc.Get(context.Background(), id)
	if err != nil || u.Body != "Jira ABC-1: Fix the thing\n\nIt is broken." {
		t.Fatalf("unit = %+v, %v", u, err)
	}
	links, err := e.svc.Links(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	for _, l := range links {
		targets = append(targets, l.Target)
	}
	slices.Sort(targets)
	if want := []string{"issue:jira:ABC-1", "jira:ABC-1"}; !slices.Equal(targets, want) {
		t.Errorf("links = %v, want %v", targets, want)
	}
	row, found, err := tickets.NewStore(e.db).ByKey(context.Background(), "jira", "ABC-1")
	if err != nil || !found || row.UnitID != id || row.Origin != tickets.OriginIssue {
		t.Errorf("row = %+v, %v, %v", row, found, err)
	}
	raw, _ := json.Marshal(out["issue"])
	var got QuickIssue
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := QuickIssue{Key: "ABC-1", URL: "https://example.atlassian.net/browse/ABC-1", Title: "Fix the thing", Type: "Bug", Status: "To Do"}
	if got != want {
		t.Errorf("issue = %+v, want %+v", got, want)
	}
}

func TestUnitCreateIssueOrphanTask(t *testing.T) {
	e := newIssueEnv(t, nil)
	out := ok(t, e.cs, "unit_create", map[string]any{"level": "task", "title": "T", "body": "b", "issue": "ABC-1"})
	id, _ := out["id"].(string)
	if !strings.HasPrefix(id, "T-") || out["issue"] == nil {
		t.Fatalf("out = %v", out)
	}
	if _, found, err := tickets.NewStore(e.db).ByKey(context.Background(), "jira", "ABC-1"); err != nil || !found {
		t.Errorf("row: %v, %v", found, err)
	}
}

func TestUnitCreateIssueRefusedForSliceAndChildTask(t *testing.T) {
	e := newIssueEnv(t, nil)
	b := ok(t, e.cs, "unit_create", map[string]any{"level": "brief", "title": "B", "body": "b"})["id"].(string)
	s := ok(t, e.cs, "unit_create", map[string]any{"level": "slice", "parent_id": b, "title": "S", "body": "b"})["id"].(string)
	before := e.count(t)
	for _, args := range []map[string]any{
		{"level": "slice", "parent_id": b, "title": "S2", "body": "b", "issue": "ABC-1"},
		{"level": "task", "parent_id": s, "title": "T", "body": "b", "acceptance_criteria": "c", "issue": "ABC-1"},
	} {
		res := fails(t, e.cs, "unit_create", args, "invalid")
		if !strings.Contains(text(res), "issue can only start a brief or a task with no parent") {
			t.Errorf("text = %q", text(res))
		}
	}
	if e.count(t) != before || len(e.fake.Calls()) != 0 {
		t.Errorf("units %d (was %d), calls %v", e.count(t), before, e.fake.Calls())
	}
}

func TestUnitCreateIssueNoSourceConfigured(t *testing.T) {
	cs, svc := newClient(t)
	res := fails(t, cs, "unit_create", briefArgs("ABC-1"), "not_configured")
	if !strings.Contains(text(res), "no issue source is configured: set issues.provider to jira") {
		t.Errorf("text = %q", text(res))
	}
	if list, err := svc.List(context.Background(), units.Filter{}); err != nil || len(list) != 0 {
		t.Fatalf("units = %v, %v", list, err)
	}
}

func TestUnitCreateIssueInvalidRef(t *testing.T) {
	e := newIssueEnv(t, nil)
	fails(t, e.cs, "unit_create", briefArgs("not a ticket"), "invalid")
	if e.count(t) != 0 {
		t.Error("a unit was created")
	}
}

func TestUnitCreateIssueNotFoundCreatesNothing(t *testing.T) {
	e := newIssueEnv(t, nil)
	res := fails(t, e.cs, "unit_create", briefArgs("ABC-404"), "not_found")
	if !strings.Contains(text(res), "ABC-404") || e.count(t) != 0 {
		t.Errorf("text %q, units %d", text(res), e.count(t))
	}
}

func TestUnitCreateIssueUnavailableCreatesNothing(t *testing.T) {
	e := newIssueEnv(t, nil)
	e.fake.FailWith(fmt.Errorf("read ABC-1: %w", issuesource.ErrUnavailable))
	fails(t, e.cs, "unit_create", briefArgs("ABC-1"), "unavailable")
	if e.count(t) != 0 {
		t.Error("a unit was created")
	}
}

func TestUnitCreateIssueAlreadyLinkedCreatesNothing(t *testing.T) {
	e := newIssueEnv(t, nil)
	first := ok(t, e.cs, "unit_create", briefArgs("ABC-1"))["id"].(string)
	res := fails(t, e.cs, "unit_create", briefArgs("ABC-1"), "invalid")
	if !strings.Contains(text(res), "ABC-1 is already linked to "+first) || e.count(t) != 1 {
		t.Errorf("text %q, units %d", text(res), e.count(t))
	}
}

type failingLinks struct{ units.Service }

func (failingLinks) AddLink(_ context.Context, _ string, _ units.LinkKind, target string) error {
	return fmt.Errorf("%w: cannot add %s", units.ErrInvalid, target)
}

func TestUnitCreateIssueAttachFailureKeepsUnitAndSaysSo(t *testing.T) {
	e := newIssueEnv(t, func(s units.Service) units.Service { return failingLinks{s} })
	res := fails(t, e.cs, "unit_create", briefArgs("ABC-1"), "invalid")
	if !strings.Contains(text(res), "created B-1 but could not link ABC-1: ") || !strings.Contains(text(res), "issue:jira:ABC-1") {
		t.Errorf("text = %q", text(res))
	}
	if _, err := e.svc.Get(context.Background(), "B-1"); err != nil {
		t.Errorf("unit gone: %v", err)
	}
	if e.count(t) != 1 {
		t.Errorf("units = %d", e.count(t))
	}
}

func TestUnitCreateIssueDescriptionMentionsIssue(t *testing.T) {
	e := newIssueEnv(t, nil)
	tool := tools(t, e.cs)["unit_create"]
	for _, want := range []string{"issue", "ticket key", "brief or a task with no parent", "nothing created"} {
		if !strings.Contains(tool.Description, want) {
			t.Errorf("description lacks %q", want)
		}
	}
	if schemaOf(t, tool).Properties["issue"] == nil {
		t.Error("no issue input")
	}
}

func TestUnitCreateWithoutIssueUnchanged(t *testing.T) {
	e := newIssueEnv(t, nil)
	out := ok(t, e.cs, "unit_create", map[string]any{"level": "brief", "title": "B", "body": "b"})
	if _, has := out["issue"]; has {
		t.Errorf("issue present: %v", out["issue"])
	}
	if len(e.fake.Calls()) != 0 {
		t.Errorf("source called: %v", e.fake.Calls())
	}
}
