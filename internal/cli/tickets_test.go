package cli

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager/jira/jiratest"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

type ticketsFixture struct {
	*statusEnv
	srv *jiratest.Server
}

// newTicketsFixture makes an initialized project wired to a stub Jira site.
func newTicketsFixture(t *testing.T) *ticketsFixture {
	t.Helper()
	e := newStatusEnv(t)
	srv := jiratest.NewServer(t, jiratest.Options{})
	t.Setenv("RALPH_TASKMANAGER_PROVIDER", "jira")
	t.Setenv("RALPH_JIRA_SITE", srv.URL())
	t.Setenv("RALPH_JIRA_USER", srv.User())
	t.Setenv("RALPH_JIRA_PROJECT", srv.Project())
	t.Setenv("JIRA_TOKEN", srv.Token())
	return &ticketsFixture{statusEnv: e, srv: srv}
}

func (f *ticketsFixture) sprint(name string) {
	f.srv.AddBoard(1, "Board", f.srv.Project())
	f.srv.AddSprint(1, 10, name, "active")
	f.t.Setenv("RALPH_JIRA_SPRINT", name)
}

func TestTicketsCommandSyncOutputAndSummary(t *testing.T) {
	f := newTicketsFixture(t)
	f.sprint("Sprint 42")
	f.with(func(svc units.Service) {
		b := f.create(svc, units.LevelBrief, "", "Brief", "")
		f.create(svc, units.LevelSlice, b, "First slice", "")
		linked := f.create(svc, units.LevelSlice, b, "Linked slice", "")
		if err := svc.AddLink(f.ctx, linked, units.LinkExternal, "jira:RAL-9"); err != nil {
			t.Fatal(err)
		}
	})
	code, out, stderr := run(t, "tickets", "sync")
	want := "created B-1 RAL-1\n" +
		"created S-1 RAL-2\n" +
		"scheduled S-1 RAL-2 in sprint \"Sprint 42\"\n" +
		"adopted S-2 RAL-9 (from its link)\n" +
		"tickets: 2 created, 1 adopted, 1 scheduled, 0 skipped, 0 failed\n"
	if code != 0 || out != want || stderr != "" {
		t.Fatalf("code %d\nout %q\nwant %q\nstderr %q", code, out, want, stderr)
	}
	code, out, _ = run(t, "tickets", "sync")
	if want := "tickets: 0 created, 0 adopted, 0 scheduled, 0 skipped, 0 failed\n"; code != 0 || out != want {
		t.Fatalf("second pass: code %d out %q", code, out)
	}
}

func TestTicketsCommandSyncJSON(t *testing.T) {
	f := newTicketsFixture(t)
	f.with(func(svc units.Service) { f.create(svc, units.LevelBrief, "", "Brief", "") })
	code, out, stderr := run(t, "tickets", "sync", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	var got map[string][]map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if len(got["created"]) != 1 || got["created"][0]["key"] != "RAL-1" || got["failed"] == nil || got["skipped"] == nil {
		t.Fatalf("json = %q", out)
	}
}

func TestTicketsCommandSyncExitsOneOnFailure(t *testing.T) {
	f := newTicketsFixture(t)
	f.with(func(svc units.Service) { f.create(svc, units.LevelBrief, "", "Brief", "") })
	f.srv.FailWith(400, `{"errorMessages":["bad request"]}`)
	code, out, _ := run(t, "tickets", "sync")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if code != 1 || len(lines) != 2 || !strings.HasPrefix(lines[0], "failed B-1 create: ") || lines[1] != "tickets: 0 created, 0 adopted, 0 scheduled, 0 skipped, 1 failed" {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestTicketsCommandListText(t *testing.T) {
	f := newTicketsFixture(t)
	code, out, _ := run(t, "tickets")
	if code != 0 || out != "no tickets yet\n" {
		t.Fatalf("empty: code %d out %q", code, out)
	}
	f.srv.AddBoard(1, "Board", f.srv.Project())
	f.t.Setenv("RALPH_JIRA_SPRINT", "Missing")
	f.with(func(svc units.Service) {
		b := f.create(svc, units.LevelBrief, "", "Brief", "")
		f.create(svc, units.LevelSlice, b, "Slice", "")
	})
	if code, _, _ := run(t, "tickets", "sync"); code != 1 {
		t.Fatalf("sync code %d", code)
	}
	code, out, _ = run(t, "tickets")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if code != 0 || len(lines) != 2 || lines[0] != "B-1  RAL-1  To Do  Feature  Brief" ||
		!strings.HasPrefix(lines[1], "S-1  RAL-2  To Do  Task  Slice  (failed schedule: ") || !strings.HasSuffix(lines[1], ")") {
		t.Fatalf("code %d out %q", code, out)
	}

	f.with(func(svc units.Service) {
		if err := svc.RemoveLink(f.ctx, "B-1", units.LinkExternal, "jira:RAL-1"); err != nil {
			t.Fatal(err)
		}
	})
	_, out, _ = run(t, "tickets")
	if first := strings.Split(out, "\n")[0]; first != "B-1  RAL-1  To Do  Feature  Brief  (detached)" {
		t.Fatalf("out %q", out)
	}
}

func TestTicketsCommandListJSON(t *testing.T) {
	f := newTicketsFixture(t)
	f.with(func(svc units.Service) { f.create(svc, units.LevelBrief, "", "Brief", "") })
	if code, _, stderr := run(t, "tickets", "sync"); code != 0 {
		t.Fatal(stderr)
	}
	code, out, _ := run(t, "tickets", "--json")
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 {
		t.Fatalf("code %d, %v: %q", code, err, out)
	}
	if len(got) != 1 {
		t.Fatalf("json = %q", out)
	}
	row := got[0]
	for k, want := range map[string]any{"unit": "B-1", "key": "RAL-1", "system": "jira", "type": "Feature", "title": "Brief", "pushed_status": "To Do", "scheduled": false, "detached": false, "attempts": float64(0)} {
		if row[k] != want {
			t.Errorf("%s = %v, want %v", k, row[k], want)
		}
	}
	for _, k := range []string{"ticket_id", "pushed_parent", "failed_step", "last_error", "synced_at"} {
		if _, ok := row[k]; !ok {
			t.Errorf("missing key %s", k)
		}
	}
}

func TestTicketsCommandNoProvider(t *testing.T) {
	newStatusEnv(t)
	for _, args := range [][]string{{"tickets"}, {"tickets", "sync"}} {
		code, out, stderr := run(t, args...)
		if want := "ralph: no task manager is configured: set taskmanager.provider to jira\n"; code != 1 || out != "" || stderr != want {
			t.Fatalf("%v: code %d out %q stderr %q", args, code, out, stderr)
		}
	}
}

func TestTicketsCommandMissingSettingNamed(t *testing.T) {
	for _, missing := range []string{"jira.site", "jira.user", "jira.project"} {
		f := newTicketsFixture(t)
		t.Setenv("RALPH_"+strings.ToUpper(strings.ReplaceAll(missing, ".", "_")), "")
		for _, args := range [][]string{{"tickets"}, {"tickets", "sync"}} {
			code, out, stderr := run(t, args...)
			if want := "ralph: " + missing + " is not set: set it to use Jira\n"; code != 1 || out != "" || stderr != want {
				t.Fatalf("%s %v: code %d out %q stderr %q", missing, args, code, out, stderr)
			}
		}
		if n := len(f.srv.Requests()); n != 0 {
			t.Fatalf("%s: %d requests", missing, n)
		}
	}
}

func TestTicketsCommandNotInitialized(t *testing.T) {
	inRepo(t)
	t.Setenv("RALPH_TASKMANAGER_PROVIDER", "jira")
	for _, args := range [][]string{{"tickets"}, {"tickets", "sync"}} {
		code, out, stderr := run(t, args...)
		if want := "ralph: this project has no ralph database: run \"ralph init\" first\n"; code != 1 || out != "" || stderr != want {
			t.Fatalf("%v: code %d out %q stderr %q", args, code, out, stderr)
		}
	}
}

func TestTicketsCommandOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"tickets"}, {"tickets", "sync"}} {
		code, _, stderr := run(t, args...)
		if code != 1 || stderr != "ralph: not in a git repository\n" {
			t.Fatalf("%v: code %d stderr %q", args, code, stderr)
		}
	}
}

func TestTicketsCommandUsesTokenFromEnvironment(t *testing.T) {
	f := newTicketsFixture(t)
	f.with(func(svc units.Service) { f.create(svc, units.LevelBrief, "", "Brief", "") })
	if code, _, stderr := run(t, "tickets", "sync"); code != 0 {
		t.Fatal(stderr)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(f.srv.User()+":"+f.srv.Token()))
	reqs := f.srv.Requests()
	if len(reqs) == 0 {
		t.Fatal("the stub saw no request")
	}
	for _, r := range reqs {
		if got := r.Header.Get("Authorization"); got != want {
			t.Fatalf("%s %s: Authorization = %q", r.Method, r.Path, got)
		}
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "ralph/") {
			t.Fatalf("User-Agent = %q", ua)
		}
	}
}
