package cli

import (
	"database/sql"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/taskmanager/jira/jiratest"
	_ "modernc.org/sqlite"
)

const (
	issueBugBody     = "The login page crashes.\n\n- open it\n- press enter"
	issueFeatureBody = "Users want to export their data.\n\n- all columns\n- UTF-8"
)

func issueUserConfig(srv *jiratest.Server, extra string) string {
	return "issues:\n  provider: jira\ntaskmanager:\n  provider: jira\njira:\n  site: " + srv.URL() +
		"\n  user: " + srv.User() + "\n  project: ABC\n  type:\n    task: \"\"\n" + extra
}

type issueE2E struct {
	*quickE2E
	srv *jiratest.Server
}

// newIssueE2E is a committed project with a bare origin, a stub GitHub and a stub Jira, and user-level
// settings that use Jira as the issue source and the task manager.
func newIssueE2E(t *testing.T) *issueE2E {
	t.Helper()
	q := newQuickE2E(t)
	q.initAndCommit(t)
	srv := jiratest.NewServer(t, jiratest.Options{Project: "ABC"})
	q.env = append(q.env, "JIRA_TOKEN="+srv.Token())
	q.writeUserConfig(t, issueUserConfig(srv, ""))
	return &issueE2E{quickE2E: q, srv: srv}
}

// newIssuePR is the loop and pull request environment with the same user-level settings.
func newIssuePR(t *testing.T) (*prE2E, *jiratest.Server) {
	t.Helper()
	var srv *jiratest.Server
	p := newPRE2EWith(t, func(q *quickE2E) {
		srv = jiratest.NewServer(t, jiratest.Options{Project: "ABC"})
		q.env = append(q.env, "JIRA_TOKEN="+srv.Token())
		q.writeUserConfig(t, issueUserConfig(srv, ""))
	})
	return p, srv
}

func (i *issueE2E) seedBug(t *testing.T) {
	t.Helper()
	i.srv.Seed(jiratest.Issue{Key: "ABC-7", Type: "Bug", Summary: "Fix the login crash", Status: "To Do", Body: issueBugBody})
}

func (i *issueE2E) writes() []string {
	var out []string
	for _, r := range i.srv.Requests() {
		isCreate := r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/rest/api/3/issue")
		if isCreate || r.Method == http.MethodPut || strings.Contains(r.Path, "/comment") {
			out = append(out, r.Method+" "+r.Path)
		}
	}
	return out
}

func storeQuery(t *testing.T, dir, query string, args ...any) string {
	t.Helper()
	d, err := sql.Open("sqlite", "file:"+filepath.Join(dir, ".ralph", "store.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	var out sql.NullString
	if err := d.QueryRow(query, args...).Scan(&out); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	return out.String
}

func countOf(t *testing.T, dir, table string) string {
	t.Helper()
	return storeQuery(t, dir, "SELECT count(*) FROM "+table)
}

func linkTargets(t *testing.T, dir, unit string) string {
	t.Helper()
	return storeQuery(t, dir, "SELECT group_concat(target, ' ') FROM (SELECT target FROM links WHERE unit_id = ? AND target LIKE '%jira:%' ORDER BY target)", unit)
}

func TestE2EIssueQuickFromTicket(t *testing.T) {
	i := newIssueE2E(t)
	i.seedBug(t)
	site := i.srv.URL()

	wantShow := "ABC-7  Bug  To Do  Fix the login crash\n" + site + "/browse/ABC-7\n\n" + issueBugBody + "\n"
	if got := i.mustRun(t, "issue", "show", "ABC-7"); got != wantShow {
		t.Errorf("issue show = %q, want %q", got, wantShow)
	}

	const branch = "ralph/abc-7-t-1-fix-the-login-crash"
	out := i.mustRun(t, "quick", "start", "--issue", "ABC-7")
	wantContains(t, "quick start", out, "started T-1: Fix the login crash", "branch: "+branch, "issue: ABC-7 Fix the login crash")
	if got := i.repo.Git("rev-parse", "--abbrev-ref", "HEAD"); got != branch {
		t.Fatalf("branch = %q, want %s", got, branch)
	}
	cs := i.connect(t, io.Discard)
	task := e2eUnit(t, cs, "T-1")
	if task["title"] != "Fix the login crash" || task["body"] != issueBugBody {
		t.Errorf("task = %v / %v", task["title"], task["body"])
	}
	if got := linkTargets(t, i.dir, "T-1"); got != "issue:jira:ABC-7 jira:ABC-7" {
		t.Errorf("links = %q", got)
	}
	if got := storeQuery(t, i.dir, "SELECT origin FROM tickets WHERE ticket_key = 'ABC-7'"); got != "issue" {
		t.Errorf("origin = %q", got)
	}

	i.repo.WriteFile("feature.txt", "hello\n")
	i.mustRun(t, "quick", "finish", "--summary", "Fixed it")
	if got := i.repo.Git("log", "-1", "--format=%s", branch); got != "ABC-7 T-1: Fix the login crash" {
		t.Errorf("commit subject = %q", got)
	}
	if !i.remoteHasBranch(branch) {
		t.Error("the branch was not pushed")
	}
	created := i.gh.creates()
	if len(created) != 1 {
		t.Fatalf("%d pull requests created, want 1", len(created))
	}
	title, _ := created[0]["title"].(string)
	body, _ := created[0]["body"].(string)
	if !strings.HasPrefix(title, "ABC-7 T-1:") {
		t.Errorf("pull request title = %q", title)
	}
	issueAt, closeAt := strings.Index(body, "Issue: ABC-7 "+site+"/browse/ABC-7\n"), strings.Index(body, "Opened by ralph for T-1.")
	if issueAt < 0 || closeAt < issueAt {
		t.Errorf("pull request body lacks the issue line before the closing line:\n%s", body)
	}

	if w := i.writes(); len(w) != 0 {
		t.Errorf("Jira received writes: %v", w)
	}
	got := readIssue(t, site, i.srv.User(), i.srv.Token(), "ABC-7")
	if got.Title != "Fix the login crash" || got.Body != issueBugBody || got.Status != "To Do" {
		t.Errorf("ticket changed: %+v", got)
	}
}

func TestE2EIssueReceiveFromTicket(t *testing.T) {
	p, srv := newIssuePR(t)
	srv.Seed(jiratest.Issue{Key: "ABC-9", Type: "Feature", Summary: "Export to CSV", Status: "To Do", Body: issueFeatureBody})
	site := srv.URL()
	before := readIssue(t, site, srv.User(), srv.Token(), "ABC-9")

	got := e2eOK(t, p.cs, "issue_get", map[string]any{"ref": "ABC-9"})
	instruction, _ := got["instruction"].(string)
	if want := "Jira ABC-9: Export to CSV\n\n" + issueFeatureBody; instruction != want {
		t.Fatalf("instruction = %q, want %q", instruction, want)
	}
	if got["type"] != "Feature" || got["key"] != "ABC-9" {
		t.Errorf("issue_get = %v", got)
	}

	made := e2eOK(t, p.cs, "unit_create", map[string]any{"level": "brief", "title": "Export to CSV", "body": instruction, "issue": "ABC-9"})
	if made["id"] != "B-1" {
		t.Fatalf("brief = %v", made)
	}
	if b := p.unit(t, "B-1")["body"]; b != instruction {
		t.Errorf("brief body = %q, want the instruction verbatim", b)
	}
	if got := linkTargets(t, p.dir, "B-1"); got != "issue:jira:ABC-9 jira:ABC-9" {
		t.Errorf("links = %q", got)
	}

	e2eOK(t, p.cs, "unit_create", map[string]any{"level": "slice", "parent_id": "B-1", "title": "Deliver the files", "body": "Deliver them."})
	for _, name := range []string{"feature", "extra"} {
		task := e2eOK(t, p.cs, "unit_create", map[string]any{
			"level": "task", "parent_id": "S-1", "title": "Write " + name,
			"body": "Create the file " + name + ".txt.\nFAKE-WRITE " + name + ".txt", "acceptance_criteria": name + ".txt exists",
		})
		e2eOK(t, p.cs, "task_set_state", map[string]any{"id": task["id"], "fingerprint": task["fingerprint"], "state": "TODO", "reason": "ready"})
	}
	p.mustRalph(t, "loop", "S-1")
	const branch = "ralph/abc-9-s-1-deliver-the-files"
	if cur := p.branch(); cur != branch {
		t.Fatalf("the loop is on branch %q, want %s", cur, branch)
	}
	if s := p.repo.Git("log", "--format=%s", "main.."+branch); !strings.Contains(s, "ABC-9 T-1: Write feature") {
		t.Errorf("commit subjects:\n%s", s)
	}
	p.mustRalph(t, "pr", "create", "S-1")
	created := p.gh.creates()
	if len(created) != 1 || created[0]["head"] != branch {
		t.Fatalf("pull requests = %v", created)
	}
	if title, _ := created[0]["title"].(string); !strings.HasPrefix(title, "ABC-9 S-1:") {
		t.Errorf("pull request title = %q", title)
	}
	if body, _ := created[0]["body"].(string); !strings.Contains(body, "Issue: ABC-9 "+site+"/browse/ABC-9") {
		t.Errorf("pull request body lacks the issue line:\n%s", body)
	}

	waitFor(t, "the slice's ticket", func() bool { return len(e2eLinks(t, p.cs, "S-1")) > 0 })
	var key string
	for _, l := range e2eLinks(t, p.cs, "S-1") {
		if k, ok := strings.CutPrefix(l, "jira:"); ok {
			key = k
		}
	}
	if key == "" {
		t.Fatalf("slice links = %v", e2eLinks(t, p.cs, "S-1"))
	}
	if s := readIssue(t, site, srv.User(), srv.Token(), key); s.Parent != "ABC-9" || s.Type != "Task" {
		t.Errorf("the slice's ticket = %+v, want a Task under ABC-9", s)
	}
	creates, puts := 0, 0
	for _, r := range srv.Requests() {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/rest/api/3/issue") {
			creates++
		}
		if r.Method == http.MethodPut && strings.HasSuffix(r.Path, "/ABC-9") {
			puts++
		}
	}
	if creates != 1 || puts != 0 {
		t.Errorf("%d tickets created and %d rewrites of ABC-9, want one ticket (the slice's) and none", creates, puts)
	}
	if after := readIssue(t, site, srv.User(), srv.Token(), "ABC-9"); after.Title != before.Title || after.Body != before.Body {
		t.Errorf("ABC-9 changed: %+v -> %+v", before, after)
	}
}

func TestE2EIssueTicketAlreadyLinked(t *testing.T) {
	i := newIssueE2E(t)
	i.seedBug(t)
	i.mustRun(t, "quick", "start", "--issue", "ABC-7")
	i.repo.Git("switch", "main")

	units, branches := countOf(t, i.dir, "units"), i.repo.Git("branch", "--list")
	code, _, stderr := i.run(t, "quick", "start", "--issue", "ABC-7")
	if code != 1 || !strings.Contains(stderr, "ABC-7 is already linked to T-1") {
		t.Errorf("second quick start: exit %d, stderr %q", code, stderr)
	}

	cs := i.connect(t, io.Discard)
	res := e2eCall(t, cs, "unit_create", map[string]any{"level": "brief", "title": "Again", "body": "b", "issue": "ABC-7"})
	if !res.IsError || !strings.Contains(e2eText(res), "ABC-7 is already linked to T-1") {
		t.Errorf("unit_create: %s", e2eText(res))
	}
	if got := countOf(t, i.dir, "units"); got != units {
		t.Errorf("units %s -> %s", units, got)
	}
	if got := i.repo.Git("branch", "--list"); got != branches {
		t.Errorf("branches:\n%s\n->\n%s", branches, got)
	}
	if got := countOf(t, i.dir, "tickets"); got != "1" {
		t.Errorf("%s ticket rows", got)
	}
}

func TestE2EIssueFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		prep func(*issueE2E)
		ref  string
		want string
		code string
	}{
		{"stub down", func(i *issueE2E) { i.srv.Close() }, "ABC-7", "ABC-7", "unavailable"},
		{"ticket missing", func(*issueE2E) {}, "ZZZ-999", "ZZZ-999", "not_found"},
		{"no issue source", func(i *issueE2E) { i.writeUserConfig(t, "issues:\n  provider: none\n") }, "ABC-7", "issues.provider", "not_configured"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			i := newIssueE2E(t)
			i.seedBug(t)
			c.prep(i)
			code, _, stderr := i.run(t, "quick", "start", "--issue", c.ref)
			if code != 1 || !strings.Contains(stderr, c.want) {
				t.Errorf("quick start: exit %d, stderr %q", code, stderr)
			}
			cs := i.connect(t, io.Discard)
			res := e2eCall(t, cs, "unit_create", map[string]any{"level": "brief", "title": "B", "body": "b", "issue": c.ref})
			if got := e2eErrorCode(t, res); got != c.code || !strings.Contains(e2eText(res), c.want) {
				t.Errorf("unit_create: code %q, %s", got, e2eText(res))
			}
			for _, table := range []string{"units", "links", "tickets"} {
				if got := countOf(t, i.dir, table); got != "0" {
					t.Errorf("%s holds %s rows", table, got)
				}
			}
			if got := i.repo.Git("branch", "--list", "ralph/*"); got != "" {
				t.Errorf("branches: %s", got)
			}
			if cur := i.repo.Git("branch", "--show-current"); cur != "main" {
				t.Errorf("checkout on %q", cur)
			}
		})
	}
}

func TestE2EIssueTicketMoves(t *testing.T) {
	i := newIssueE2E(t)
	i.srv.Seed(jiratest.Issue{Key: "ABC-5", Type: "Feature", Summary: "Rework export", Status: "To Do", Body: "Make it faster."})
	cs := i.connect(t, io.Discard)
	e2eOK(t, cs, "unit_create", map[string]any{"level": "brief", "title": "Rework export", "body": "Jira ABC-5: Rework export\n\nMake it faster.", "issue": "ABC-5"})
	s := e2eCreate(t, cs, "slice", "B-1", "The slice", "What.")
	task := e2eCreate(t, cs, "task", s, "The task", "How.")
	moves := func() int {
		n := 0
		for _, r := range i.srv.Requests() {
			if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/ABC-5/transitions") {
				n++
			}
		}
		return n
	}

	i.mustRun(t, "tickets", "sync")
	if moves() != 0 || i.srv.Status("ABC-5") != "To Do" {
		t.Fatalf("the ticket moved before any work: %d moves, in %q", moves(), i.srv.Status("ABC-5"))
	}
	e2eSetState(t, cs, task, "TODO")
	e2eSetState(t, cs, task, "IN_PROGRESS")
	waitFor(t, "In Progress", func() bool { return i.srv.Status("ABC-5") == "In Progress" })
	i.mustRun(t, "tickets", "sync")
	if moves() != 1 {
		t.Errorf("%d moves of ABC-5, want 1", moves())
	}

	i.srv.Edit("ABC-5", taskmanager.Fields{Title: "Retitled by a person", Body: "A different text."})
	r := i.mustRun(t, "tickets", "check")
	if r != "tickets: no differences\n" {
		t.Errorf("an edit of the ticket's text was reported:\n%s", r)
	}
	i.mustRun(t, "tickets", "sync")
	for _, w := range i.writes() {
		if w != "POST /rest/api/3/issue" {
			t.Errorf("Jira received %s, want only the slice's ticket created", w)
		}
	}

	i.srv.Delete("ABC-5")
	r = i.mustRun(t, "tickets", "check")
	wantContains(t, "tickets check", r, "B-1 ABC-5: ticket no longer exists in Jira")
}

func TestE2EIssueBranchNamesKept(t *testing.T) {
	p, srv := newIssuePR(t)
	srv.Seed(jiratest.Issue{Key: "ABC-9", Type: "Feature", Summary: "Export to CSV", Body: issueFeatureBody})
	p.deliver(t)

	e2eOK(t, p.cs, "link_add", map[string]any{"id": "B-1", "kind": "external", "target": "issue:jira:ABC-9"})
	p.mustRalph(t, "pr", "create", "S-1")
	created := p.gh.creates()
	if len(created) != 1 || created[0]["head"] != loopBranch {
		t.Fatalf("pull requests = %v, want one from %s", created, loopBranch)
	}
	if p.remoteHead(t, loopBranch) == "" || p.remoteHead(t, "ralph/abc-9-s-1-deliver-the-files") != "" {
		t.Error("the branch was renamed on the remote")
	}
}

func TestE2EIssueSettingsAreUserOnly(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	srv := jiratest.NewServer(t, jiratest.Options{Project: "ABC"})
	e.env = append(e.env, "JIRA_TOKEN="+srv.Token())
	srv.Seed(jiratest.Issue{Key: "ABC-1", Summary: "Anything"})
	project := filepath.Join(e.dir, ".ralph", "project.yaml")

	appendFile(t, project, "issues:\n  provider: jira\njira:\n  site: "+srv.URL()+"\n  user: "+srv.User()+"\n")
	r := e.run(t, "issue", "show", "ABC-1")
	if r.code == 0 || !strings.Contains(r.stderr, "jira.site") {
		t.Errorf("issue show: exit %d, stderr %q", r.code, r.stderr)
	}
	wantContains(t, "stderr", r.stderr, `"jira.site" is user-only and was ignored in the project file`)
	if strings.Contains(r.stderr, `"issues.provider"`) {
		t.Errorf("issues.provider was ignored:\n%s", r.stderr)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("%d requests reached the site named in the project file", n)
	}

	e2 := newE2EEnv(t)
	e2.init(t)
	appendFile(t, filepath.Join(e2.dir, ".ralph", "project.yaml"), "issues:\n  provider: jira\n")
	if r := e2.run(t, "status"); r.code != 0 || strings.Contains(r.stderr, "issues.provider") {
		t.Errorf("status: exit %d, stderr %q", r.code, r.stderr)
	}
	r = e2.run(t, "issue", "show", "ABC-1")
	if r.code == 0 || !strings.Contains(r.stderr, "jira.site is not set") || strings.Contains(r.stderr, "no issue source is configured") {
		t.Errorf("issue show with only the provider: exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestE2EIssueDefaultsUnchangedWithoutTicket(t *testing.T) {
	t.Run("quick work", func(t *testing.T) {
		i := newIssueE2E(t)
		i.mustRun(t, "quick", "start", "Add feature file")
		const branch = "ralph/t-1-add-feature-file"
		if got := i.repo.Git("branch", "--show-current"); got != branch {
			t.Fatalf("branch = %q, want %s", got, branch)
		}
		i.repo.WriteFile("feature.txt", "hello\n")
		i.mustRun(t, "quick", "finish")
		if got := i.repo.Git("log", "-1", "--format=%s", branch); got != "T-1: Add feature file" {
			t.Errorf("commit subject = %q", got)
		}
		created := i.gh.creates()
		if len(created) != 1 || created[0]["title"] != "T-1: Add feature file" {
			t.Fatalf("pull requests = %v", created)
		}
		if body, _ := created[0]["body"].(string); strings.Contains(body, "Issue:") {
			t.Errorf("pull request body names an issue:\n%s", body)
		}
	})

	t.Run("loop and pull request", func(t *testing.T) {
		p, _ := newIssuePR(t)
		p.open(t)
		subjects := strings.Fields(strings.ReplaceAll(p.repo.Git("log", "--format=%s", "main.."+loopBranch), " ", "_"))
		for _, want := range []string{"T-1:_Write_feature", "T-2:_Write_extra"} {
			found := false
			for _, s := range subjects {
				found = found || s == want
			}
			if !found {
				t.Errorf("commit subjects %v lack %q", subjects, want)
			}
		}
		created := p.gh.creates()
		if len(created) != 1 || created[0]["head"] != loopBranch || created[0]["title"] != "S-1: Deliver the files" {
			t.Fatalf("pull requests = %v", created)
		}
		if body, _ := created[0]["body"].(string); strings.Contains(body, "Issue:") {
			t.Errorf("pull request body names an issue:\n%s", body)
		}
	})
}
