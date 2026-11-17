package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/adf"
	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/taskmanager/jira/jiratest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
)

const e2eSprint = "Sprint 1"

type jiraE2E struct {
	*e2eEnv
	srv       *jiratest.Server
	projectID string
}

// newJiraE2E makes an initialized project whose user-level configuration points at a stub Jira site. extra is
// YAML appended to the user file.
func newJiraE2E(t *testing.T, sprint bool, extra string) *jiraE2E {
	t.Helper()
	e := newE2EEnv(t)
	e.init(t)
	srv := jiratest.NewServer(t, jiratest.Options{Project: "ABC"})
	e.env = append(e.env, "JIRA_TOKEN="+srv.Token())
	cfg := "taskmanager:\n  provider: jira\njira:\n  site: " + srv.URL() + "\n  user: " + srv.User() + "\n  project: ABC\n"
	if sprint {
		srv.AddBoard(1, "Board", "ABC")
		srv.AddSprint(1, 10, e2eSprint, "active")
		cfg += "  sprint: " + e2eSprint + "\n"
	}
	e.writeUserConfig(t, cfg+extra)
	raw, err := os.ReadFile(filepath.Join(e.dir, ".ralph", "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s+id:\s*(\S+)`).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("no project id in %s", raw)
	}
	return &jiraE2E{e2eEnv: e, srv: srv, projectID: string(m[1])}
}

func (j *jiraE2E) ref(unit string) string { return "ralph-" + j.projectID + "-" + unit }

func (j *jiraE2E) mustRun(t *testing.T, args ...string) e2eRun {
	t.Helper()
	r := j.run(t, args...)
	if r.code != 0 {
		t.Fatalf("ralph %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r
}

func (j *jiraE2E) requestsMatching(method, suffix string) int {
	n := 0
	for _, r := range j.srv.Requests() {
		if r.Method == method && strings.HasSuffix(r.Path, suffix) {
			n++
		}
	}
	return n
}

func (j *jiraE2E) creates() int { return j.requestsMatching(http.MethodPost, "/rest/api/3/issue") }

type e2eIssue struct {
	Key    string
	Type   string
	Title  string
	Status string
	Parent string
	Labels []string
	Body   string
}

// issue reads a ticket from the stub the way a person looking at Jira would.
func (j *jiraE2E) issue(t *testing.T, key string) e2eIssue {
	t.Helper()
	return readIssue(t, j.srv.URL(), j.srv.User(), j.srv.Token(), key)
}

func readIssue(t *testing.T, site, user, token, key string) e2eIssue {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, site+"/rest/api/3/issue/"+key, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(user, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading %s: %d %s", key, resp.StatusCode, raw)
	}
	var got struct {
		Key    string `json:"key"`
		Fields struct {
			Summary     string          `json:"summary"`
			Description json.RawMessage `json:"description"`
			Labels      []string        `json:"labels"`
			IssueType   struct{ Name string }
			Status      struct{ Name string }
			Parent      *struct{ Key string }
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	out := e2eIssue{Key: got.Key, Type: got.Fields.IssueType.Name, Title: got.Fields.Summary, Status: got.Fields.Status.Name, Labels: got.Fields.Labels}
	if got.Fields.Parent != nil {
		out.Parent = got.Fields.Parent.Key
	}
	if len(got.Fields.Description) > 0 && string(got.Fields.Description) != "null" {
		doc, err := adf.ParseJSON(got.Fields.Description)
		if err != nil {
			t.Fatalf("description of %s: %v", key, err)
		}
		out.Body = adf.ToMarkdown(doc)
	}
	return out
}

func (j *jiraE2E) status(t *testing.T, key string) string { return j.srv.Status(key) }

// waitFor polls until cond holds; the automatic push of the MCP process runs behind the tool call.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// links returns the external links of a unit.
func e2eLinks(t *testing.T, cs *mcp.ClientSession, id string) []string {
	t.Helper()
	raw, _ := e2eOK(t, cs, "unit_get", map[string]any{"id": id})["links"].([]any)
	var out []string
	for _, l := range raw {
		m, _ := l.(map[string]any)
		if m["kind"] == "external" {
			out = append(out, m["target"].(string))
		}
	}
	return out
}

func e2eSetState(t *testing.T, cs *mcp.ClientSession, id, state string) {
	t.Helper()
	e2eOK(t, cs, "task_set_state", map[string]any{"id": id, "fingerprint": e2eFingerprint(t, cs, id), "state": state, "reason": "test"})
}

func e2eRetitle(t *testing.T, cs *mcp.ClientSession, id, title string) {
	t.Helper()
	e2eOK(t, cs, "unit_update", map[string]any{"id": id, "fingerprint": e2eFingerprint(t, cs, id), "title": title})
}

func e2eCreate(t *testing.T, cs *mcp.ClientSession, level, parent, title, body string) string {
	t.Helper()
	args := map[string]any{"level": level, "title": title}
	if parent != "" {
		args["parent_id"] = parent
	}
	if body != "" {
		args["body"] = body
	}
	if level == "task" {
		args["acceptance_criteria"] = "it works"
	}
	return e2eOK(t, cs, "unit_create", args)["id"].(string)
}

const (
	briefBody = "The brief says:\n\n- first point\n- second point\n"
	sliceBody = "A slice with **bold** words and a [link](https://example.com/x).\n\n1. one\n2. two\n"
)

func TestE2ETicketsCreateAndLink(t *testing.T) {
	j := newJiraE2E(t, true, "")
	cs := j.connect(t, io.Discard)
	b := e2eCreate(t, cs, "brief", "", "Ship it", briefBody)
	s1 := e2eCreate(t, cs, "slice", b, "First slice", sliceBody)
	s2 := e2eCreate(t, cs, "slice", b, "Second slice", "Plain body.\n")
	t1 := e2eCreate(t, cs, "task", s1, "First task", "do it")
	e2eCreate(t, cs, "task", s2, "Second task", "do it too")
	if b != "B-1" || s1 != "S-1" || s2 != "S-2" || t1 != "T-1" {
		t.Fatalf("ids %s %s %s %s", b, s1, s2, t1)
	}

	waitFor(t, "the three tickets", func() bool { return e2eLinksCount(t, cs, b, s1, s2) == 3 })
	keys := map[string]string{}
	for _, id := range []string{b, s1, s2} {
		l := e2eLinks(t, cs, id)
		if len(l) != 1 || !strings.HasPrefix(l[0], "jira:ABC-") {
			t.Fatalf("links of %s = %v", id, l)
		}
		keys[id] = strings.TrimPrefix(l[0], "jira:")
	}
	for _, id := range []string{"T-1", "T-2"} {
		if l := e2eLinks(t, cs, id); len(l) != 0 {
			t.Errorf("task %s has links %v", id, l)
		}
	}

	brief := j.issue(t, keys[b])
	if brief.Type != "Feature" || brief.Title != "Ship it" || brief.Parent != "" || brief.Body != strings.TrimSpace(briefBody) {
		t.Errorf("brief ticket = %+v", brief)
	}
	if !sameSet(brief.Labels, []string{"ralph", j.ref(b)}) {
		t.Errorf("brief labels = %v", brief.Labels)
	}
	for id, wantBody := range map[string]string{s1: strings.TrimSpace(sliceBody), s2: "Plain body."} {
		got := j.issue(t, keys[id])
		if got.Type != "Task" || got.Parent != keys[b] || got.Body != wantBody {
			t.Errorf("ticket of %s = %+v, want parent %s and body %q", id, got, keys[b], wantBody)
		}
		if !sameSet(got.Labels, []string{"ralph", j.ref(id)}) {
			t.Errorf("labels of %s = %v", id, got.Labels)
		}
	}
	waitFor(t, "the sprint", func() bool { return len(j.srv.SprintIssues(10)) == 2 })
	if got := j.srv.SprintIssues(10); !sameSet(got, []string{keys[s1], keys[s2]}) {
		t.Errorf("sprint holds %v", got)
	}
	if n := j.creates(); n != 3 {
		t.Errorf("%d tickets created, want one per brief and slice", n)
	}

	r := j.mustRun(t, "tickets")
	for _, id := range []string{b, s1, s2} {
		if !strings.Contains(r.stdout, id+"  "+keys[id]+"  To Do  ") {
			t.Errorf("ralph tickets lacks %s %s:\n%s", id, keys[id], r.stdout)
		}
	}
	if strings.Contains(r.stdout, "T-1") {
		t.Errorf("ralph tickets lists a task:\n%s", r.stdout)
	}

	j.srv.ResetRequests()
	sync := j.mustRun(t, "tickets", "sync")
	if !strings.Contains(sync.stdout, "tickets: 0 created, 0 adopted, 0 updated") || j.creates() != 0 {
		t.Errorf("second sync:\n%s\ncreates %d", sync.stdout, j.creates())
	}
	e2eCreate(t, cs, "task", s1, "Third task", "more")
	time.Sleep(300 * time.Millisecond)
	if j.creates() != 0 {
		t.Errorf("a new task made a ticket")
	}
}

func e2eLinksCount(t *testing.T, cs *mcp.ClientSession, ids ...string) int {
	n := 0
	for _, id := range ids {
		if len(e2eLinks(t, cs, id)) > 0 {
			n++
		}
	}
	return n
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func (j *jiraE2E) singleSlice(t *testing.T, cs *mcp.ClientSession) (string, string) {
	t.Helper()
	b := e2eCreate(t, cs, "brief", "", "Ship it", "Why.")
	s := e2eCreate(t, cs, "slice", b, "The slice", "What.")
	e2eCreate(t, cs, "task", s, "The task", "How.")
	waitFor(t, "the tickets", func() bool { return len(e2eLinks(t, cs, b)) == 1 && len(e2eLinks(t, cs, s)) == 1 })
	return strings.TrimPrefix(e2eLinks(t, cs, b)[0], "jira:"), strings.TrimPrefix(e2eLinks(t, cs, s)[0], "jira:")
}

func TestE2ETicketsStatesFollowWork(t *testing.T) {
	t.Run("default mapping", func(t *testing.T) {
		j := newJiraE2E(t, false, "")
		cs := j.connect(t, io.Discard)
		_, key := j.singleSlice(t, cs)
		if got := j.status(t, key); got != "To Do" {
			t.Fatalf("new ticket in %q", got)
		}
		e2eSetState(t, cs, "T-1", "TODO")
		e2eSetState(t, cs, "T-1", "IN_PROGRESS")
		waitFor(t, "In Progress", func() bool { return j.status(t, key) == "In Progress" })

		e2eOK(t, cs, "task_set_state", map[string]any{"id": "T-1", "fingerprint": e2eFingerprint(t, cs, "T-1"), "state": "BLOCKED", "reason": "waiting"})
		r := j.mustRun(t, "tickets", "sync")
		if !strings.Contains(r.stdout, "0 moved") || j.status(t, key) != "In Progress" {
			t.Errorf("a blocked task moved the ticket: %q, in %q", r.stdout, j.status(t, key))
		}
		e2eSetState(t, cs, "T-1", "TODO")
		e2eSetState(t, cs, "T-1", "IN_PROGRESS")
		e2eSetState(t, cs, "T-1", "DONE")
		waitFor(t, "Done", func() bool { return j.status(t, key) == "Done" })
	})

	t.Run("cancelled leaves the ticket", func(t *testing.T) {
		j := newJiraE2E(t, false, "")
		cs := j.connect(t, io.Discard)
		_, key := j.singleSlice(t, cs)
		e2eSetState(t, cs, "T-1", "TODO")
		e2eSetState(t, cs, "T-1", "IN_PROGRESS")
		waitFor(t, "In Progress", func() bool { return j.status(t, key) == "In Progress" })
		e2eOK(t, cs, "task_set_state", map[string]any{"id": "T-1", "fingerprint": e2eFingerprint(t, cs, "T-1"), "state": "CANCELLED", "reason": "dropped"})
		r := j.mustRun(t, "tickets", "sync")
		if !strings.Contains(r.stdout, "0 moved") || j.status(t, key) != "In Progress" {
			t.Errorf("a cancelled task moved the ticket: %q, in %q", r.stdout, j.status(t, key))
		}
	})

	const states = "  states:\n    - IN_PROGRESS=Doing\n    - DONE=Finished\n    - READY=Backlog\n    - ARTICULATING=Backlog\n    - EMPTY=Backlog\n"
	chain := map[string][]string{"Backlog": {"Selected"}, "Selected": {"Doing"}, "Doing": {"Finished"}}

	t.Run("states and flow", func(t *testing.T) {
		j := newJiraE2E(t, false, states+"  flow:\n    - Backlog\n    - Selected\n    - Doing\n    - Finished\n")
		j.srv.SetWorkflow("Backlog", chain)
		cs := j.connect(t, io.Discard)
		_, key := j.singleSlice(t, cs)
		if got := j.status(t, key); got != "Backlog" {
			t.Fatalf("new ticket in %q", got)
		}
		e2eSetState(t, cs, "T-1", "TODO")
		e2eSetState(t, cs, "T-1", "IN_PROGRESS")
		waitFor(t, "Doing", func() bool { return j.status(t, key) == "Doing" })
		if r := j.mustRun(t, "tickets", "sync"); !strings.Contains(r.stdout, "moved B-1") && !strings.Contains(r.stdout, "0 failed") {
			t.Errorf("sync = %q", r.stdout)
		}
		e2eSetState(t, cs, "T-1", "DONE")
		waitFor(t, "Finished", func() bool { return j.status(t, key) == "Finished" })
		if row := j.mustRun(t, "tickets").stdout; !strings.Contains(row, "  Finished  ") {
			t.Errorf("ralph tickets = %q", row)
		}
	})

	t.Run("no path", func(t *testing.T) {
		j := newJiraE2E(t, false, states+"  type:\n    brief: \"\"\n")
		j.srv.SetWorkflow("Backlog", map[string][]string{"Backlog": {"Selected"}, "Selected": {"Backlog"}})
		cs := j.connect(t, io.Discard)
		b := e2eCreate(t, cs, "brief", "", "Ship it", "Why.")
		s := e2eCreate(t, cs, "slice", b, "The slice", "What.")
		e2eCreate(t, cs, "task", s, "The task", "How.")
		waitFor(t, "the ticket", func() bool { return len(e2eLinks(t, cs, s)) == 1 })
		key := strings.TrimPrefix(e2eLinks(t, cs, s)[0], "jira:")
		e2eSetState(t, cs, "T-1", "TODO")
		e2eSetState(t, cs, "T-1", "IN_PROGRESS")
		r := j.run(t, "tickets", "sync")
		if r.code != 1 || !strings.Contains(r.stdout, "failed S-1 move: ") {
			t.Fatalf("sync: exit %d\n%s%s", r.code, r.stdout, r.stderr)
		}
		if got := j.status(t, key); got != "Backlog" {
			t.Errorf("the ticket moved to %q", got)
		}
		list := j.mustRun(t, "tickets").stdout
		if !strings.Contains(list, "failed move") || !strings.Contains(list, "Selected") {
			t.Errorf("ralph tickets lacks the failure and the reachable statuses:\n%s", list)
		}
		if st := j.mustRun(t, "status").stdout; !strings.Contains(st, "tickets: 1 failed to sync") {
			t.Errorf("status = %q", st)
		}

		j.srv.SetWorkflow("", map[string][]string{"Backlog": {"Selected", "Doing"}, "Doing": {"Finished"}})
		j.mustRun(t, "tickets", "sync", "--json")
		if got := j.status(t, key); got != "Doing" {
			t.Errorf("after the fix the ticket is in %q", got)
		}
		if st := j.mustRun(t, "status").stdout; strings.Contains(st, "tickets:") {
			t.Errorf("status after the fix = %q", st)
		}
	})
}

func (j *jiraE2E) setEnv(key, value string) {
	var env []string
	for _, kv := range j.env {
		if !strings.HasPrefix(kv, key+"=") {
			env = append(env, kv)
		}
	}
	if value != "" {
		env = append(env, key+"="+value)
	}
	j.env = env
}

func TestE2ETicketsAutomaticAfterWrites(t *testing.T) {
	j := newJiraE2E(t, false, "")
	j.setEnv("RALPH_JIRA_AUTO_SYNC", "false")
	cs := j.connect(t, io.Discard)
	b := e2eCreate(t, cs, "brief", "", "Ship it", "Why.")
	s := e2eCreate(t, cs, "slice", b, "The slice", "What.")
	e2eCreate(t, cs, "task", s, "The task", "How.")
	other := e2eCreate(t, cs, "slice", b, "The other slice", "Else.")
	doc := e2eOK(t, cs, "doc_create", map[string]any{"kind": "design", "title": "Design", "body": "How."})["id"].(string)
	time.Sleep(500 * time.Millisecond)
	if n := len(j.srv.Requests()); n != 0 {
		t.Fatalf("auto_sync false: %d requests from tool calls", n)
	}
	j.mustRun(t, "docs", "link", doc, s)
	j.mustRun(t, "status")
	if n := len(j.srv.Requests()); n != 0 {
		t.Fatalf("auto_sync false: %d requests from commands", n)
	}
	if r := j.mustRun(t, "tickets", "sync"); !strings.Contains(r.stdout, "3 created") {
		t.Fatalf("tickets sync = %q", r.stdout)
	}
	key := strings.TrimPrefix(e2eLinks(t, cs, s)[0], "jira:")
	otherKey := strings.TrimPrefix(e2eLinks(t, cs, other)[0], "jira:")

	j.setEnv("RALPH_JIRA_AUTO_SYNC", "")
	j.srv.ResetRequests()
	for _, args := range [][]string{{"status"}, {"status", "--all"}, {"docs", "links", s}, {"tickets"}} {
		j.mustRun(t, args...)
	}
	if n := len(j.srv.Requests()); n != 0 {
		t.Fatalf("read commands made %d requests", n)
	}

	cs2 := j.connect(t, io.Discard)
	e2eRetitle(t, cs2, s, "Retitled by a tool")
	waitFor(t, "the tool's title", func() bool { return j.issue(t, key).Title == "Retitled by a tool" })

	j.setEnv("RALPH_JIRA_AUTO_SYNC", "false")
	cs3 := j.connect(t, io.Discard)
	e2eRetitle(t, cs3, s, "Retitled with sync off")
	time.Sleep(500 * time.Millisecond)
	if got := j.issue(t, key).Title; got != "Retitled by a tool" {
		t.Fatalf("auto_sync false pushed %q", got)
	}
	j.setEnv("RALPH_JIRA_AUTO_SYNC", "")
	r := j.mustRun(t, "docs", "unlink", doc, s)
	if r.stderr != "" {
		t.Errorf("stderr = %q", r.stderr)
	}
	if got := j.issue(t, key).Title; got != "Retitled with sync off" {
		t.Fatalf("a write command did not push: %q", got)
	}

	j.srv.RejectCredentials()
	e2eRetitle(t, cs3, s, "Retitled while down")
	e2eRetitle(t, cs3, other, "The other retitled while down")
	r = j.run(t, "docs", "link", doc, s)
	if r.code != 0 || strings.Count(r.stderr, "ralph: warning:") != 1 || !strings.Contains(r.stderr, `ralph: warning: 1 ticket failed to sync; run "ralph tickets"`) {
		t.Fatalf("while down: exit %d stderr %q", r.code, r.stderr)
	}
	if got := e2eOK(t, cs, "unit_get", map[string]any{"id": s})["title"]; got != "Retitled while down" {
		t.Errorf("the work was lost: title %v", got)
	}
	j.srv.Heal()
	j.mustRun(t, "docs", "unlink", doc, s)
	if got := j.issue(t, otherKey).Title; got != "The other retitled while down" {
		t.Errorf("the backlog was not pushed: %q", got)
	}
	j.mustRun(t, "tickets", "sync")
	if got := j.issue(t, key).Title; got != "Retitled while down" {
		t.Errorf("ralph tickets sync did not push: %q", got)
	}
}

// moveInJira changes the status of a ticket the way a person does, through its transitions.
func (j *jiraE2E) moveInJira(t *testing.T, key, status string) {
	t.Helper()
	call := func(method, path string, body any) []byte {
		var rd io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			rd = strings.NewReader(string(raw))
		}
		req, err := http.NewRequest(method, j.srv.URL()+"/rest/api/3/issue/"+key+path, rd)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth(j.srv.User(), j.srv.Token())
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
		}
		return raw
	}
	var list struct {
		Transitions []struct {
			ID string
			To struct{ Name string }
		}
	}
	if err := json.Unmarshal(call(http.MethodGet, "/transitions", nil), &list); err != nil {
		t.Fatal(err)
	}
	for _, tr := range list.Transitions {
		if tr.To.Name == status {
			call(http.MethodPost, "/transitions", map[string]any{"transition": map[string]string{"id": tr.ID}})
			return
		}
	}
	t.Fatalf("no transition of %s to %s", key, status)
}

func e2eUnit(t *testing.T, cs *mcp.ClientSession, id string) map[string]any {
	t.Helper()
	return e2eOK(t, cs, "unit_get", map[string]any{"id": id})
}

func TestE2ETicketsEditedInJira(t *testing.T) {
	j := newJiraE2E(t, false, "")
	cs := j.connect(t, io.Discard)
	b := e2eCreate(t, cs, "brief", "", "Ship it", "Why.")
	slices := map[string]string{}
	keys := map[string]string{}
	for _, name := range []string{"S-1", "S-2", "S-3", "S-4"} {
		id := e2eCreate(t, cs, "slice", b, "Slice "+name, "Body of "+name+".")
		if id != name {
			t.Fatalf("created %s, want %s", id, name)
		}
		slices[name] = "Body of " + name + "."
	}
	waitFor(t, "the tickets", func() bool { return e2eLinksCount(t, cs, b, "S-1", "S-2", "S-3", "S-4") == 5 })
	for id := range slices {
		keys[id] = strings.TrimPrefix(e2eLinks(t, cs, id)[0], "jira:")
	}

	j.srv.Edit(keys["S-1"], taskmanager.Fields{Title: "Edited in Jira", Body: slices["S-1"]})
	j.moveInJira(t, keys["S-1"], "In Progress")
	j.srv.Edit(keys["S-2"], taskmanager.Fields{Title: "Also edited in Jira", Body: "A new body."})
	j.srv.Delete(keys["S-3"])
	j.srv.Delete(keys["S-4"])

	r := j.mustRun(t, "tickets", "check")
	for _, want := range []string{
		"S-1 " + keys["S-1"] + ": title and status differ in Jira",
		"S-2 " + keys["S-2"] + ": title and body differ in Jira",
		"S-3 " + keys["S-3"] + ": ticket no longer exists in Jira",
		"S-4 " + keys["S-4"] + ": ticket no longer exists in Jira",
		"tickets: 4 with differences",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("check lacks %q:\n%s", want, r.stdout)
		}
	}

	const line = "tickets: 4 with differences in Jira; run ralph tickets"
	if st := j.mustRun(t, "status").stdout; !strings.Contains(st, line) {
		t.Errorf("status lacks the notice:\n%s", st)
	}
	if mon := j.mustRun(t, "monitor", "--no-poll").stdout; !strings.Contains(mon, line) {
		t.Errorf("monitor lacks the notice:\n%s", mon)
	}
	cs2 := j.connect(t, io.Discard)
	if n := noticeCount(e2eCall(t, cs2, "status", nil), "tickets_need_attention"); n != 1 {
		t.Errorf("first call of a session: %d notices", n)
	}
	if n := noticeCount(e2eCall(t, cs2, "status", nil), "tickets_need_attention"); n != 0 {
		t.Errorf("second call of the session: %d notices", n)
	}

	e2eRetitle(t, cs, "S-1", "Ralph's edit")
	if r := j.mustRun(t, "tickets", "sync"); !strings.Contains(r.stdout, "held S-1 "+keys["S-1"]) {
		t.Errorf("sync = %q", r.stdout)
	}
	if got := j.issue(t, keys["S-1"]); got.Title != "Edited in Jira" || got.Status != "In Progress" {
		t.Errorf("a held ticket was pushed: %+v", got)
	}

	before := e2eUnit(t, cs, "S-1")
	r = j.mustRun(t, "tickets", "accept", "S-1")
	if !strings.Contains(r.stdout, `title: "Ralph's edit" -> "Edited in Jira"`) || !strings.Contains(r.stdout, "run again with --yes to apply") {
		t.Errorf("accept without --yes:\n%s", r.stdout)
	}
	if after := e2eUnit(t, cs, "S-1"); after["fingerprint"] != before["fingerprint"] || after["title"] != "Ralph's edit" {
		t.Errorf("accept without --yes changed the unit: %v", after)
	}
	j.srv.ResetRequests()
	r = j.mustRun(t, "tickets", "accept", "S-1", "--yes")
	if !strings.HasPrefix(r.stdout, "accepted S-1 ") {
		t.Errorf("accept --yes:\n%s", r.stdout)
	}
	after := e2eUnit(t, cs, "S-1")
	if after["title"] != "Edited in Jira" || len(after["notes"].([]any)) != len(before["notes"].([]any))+1 {
		t.Errorf("unit after accept: %v", after)
	}
	if r := j.mustRun(t, "tickets", "sync"); !strings.Contains(r.stdout, "0 updated") || strings.Contains(r.stdout, "held S-1") {
		t.Errorf("sync after accept = %q", r.stdout)
	}
	if got := j.issue(t, keys["S-1"]); got.Title != "Edited in Jira" || got.Body != slices["S-1"] {
		t.Errorf("accept pushed: %+v", got)
	}

	s2 := e2eUnit(t, cs, "S-2")
	r = j.mustRun(t, "tickets", "overwrite", "S-2", "--yes")
	if !strings.HasPrefix(r.stdout, "overwrote "+keys["S-2"]+" ") {
		t.Errorf("overwrite --yes:\n%s", r.stdout)
	}
	if got := j.issue(t, keys["S-2"]); got.Title != "Slice S-2" || got.Body != slices["S-2"] {
		t.Errorf("overwrite did not push ralph's values: %+v", got)
	}
	if after := e2eUnit(t, cs, "S-2"); after["title"] != "Slice S-2" || len(after["notes"].([]any)) != len(s2["notes"].([]any))+1 {
		t.Errorf("S-2 after overwrite: %v", after)
	}
	if r := j.mustRun(t, "tickets", "sync"); !strings.Contains(r.stdout, "0 updated") {
		t.Errorf("sync after overwrite = %q", r.stdout)
	}

	j.mustRun(t, "tickets", "accept", "S-3", "--yes")
	if l := e2eLinks(t, cs, "S-3"); len(l) != 0 {
		t.Errorf("S-3 still linked: %v", l)
	}
	j.srv.ResetRequests()
	j.mustRun(t, "tickets", "sync")
	if j.creates() != 0 {
		t.Errorf("a detached unit got a new ticket")
	}
	if list := j.mustRun(t, "tickets").stdout; !strings.Contains(list, "S-3  "+keys["S-3"]) || !strings.Contains(list, "(detached)") {
		t.Errorf("ralph tickets:\n%s", list)
	}

	j.mustRun(t, "tickets", "overwrite", "S-4", "--yes")
	l := e2eLinks(t, cs, "S-4")
	if len(l) != 1 || l[0] == "jira:"+keys["S-4"] {
		t.Fatalf("S-4 links after overwrite: %v", l)
	}
	if got := j.issue(t, strings.TrimPrefix(l[0], "jira:")); got.Title != "Slice S-4" || !sameSet(got.Labels, []string{"ralph", j.ref("S-4")}) {
		t.Errorf("recreated ticket: %+v", got)
	}
	if st := j.mustRun(t, "status").stdout; strings.Contains(st, "with differences in Jira") {
		t.Errorf("status still shows differences:\n%s", st)
	}
}

func TestE2ETicketsAdoptAndDetach(t *testing.T) {
	j := newJiraE2E(t, false, "")
	j.setEnv("RALPH_JIRA_AUTO_SYNC", "false")
	cs := j.connect(t, io.Discard)
	b := e2eCreate(t, cs, "brief", "", "Ship it", "Why.")
	linked := e2eCreate(t, cs, "slice", b, "Linked slice", "Linked body.")
	detached := e2eCreate(t, cs, "slice", b, "Detached slice", "Detached body.")
	crashed := e2eCreate(t, cs, "slice", b, "Crashed slice", "Crashed body.")

	existing := j.srv.Seed(jiratest.Issue{Key: "ABC-99", Type: "Task", Summary: "Written in Jira", Body: "Old body."})
	e2eOK(t, cs, "link_add", map[string]any{"id": linked, "kind": "external", "target": "jira:" + existing})
	leftover := j.srv.Seed(jiratest.Issue{Type: "Task", Summary: "Left by a crash", Labels: []string{"ralph", j.ref(crashed)}})

	r := j.mustRun(t, "tickets", "sync")
	for _, want := range []string{"adopted " + linked + " " + existing, "adopted " + crashed + " " + leftover, "created " + b + " ", "created " + detached + " ", "2 adopted"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("sync lacks %q:\n%s", want, r.stdout)
		}
	}
	if n := j.creates(); n != 2 {
		t.Errorf("%d tickets created, want the brief's and the detached slice's", n)
	}
	if r := j.mustRun(t, "tickets", "sync"); !strings.Contains(r.stdout, "2 updated") || !strings.Contains(r.stdout, "0 created") {
		t.Errorf("second sync:\n%s", r.stdout)
	}
	if got := j.issue(t, existing); got.Title != "Linked slice" || got.Body != "Linked body." || got.Key != "ABC-99" {
		t.Errorf("adopted ticket = %+v", got)
	}
	if got := j.issue(t, leftover); got.Title != "Crashed slice" || got.Body != "Crashed body." {
		t.Errorf("crash ticket = %+v", got)
	}
	if l := e2eLinks(t, cs, crashed); len(l) != 1 || l[0] != "jira:"+leftover {
		t.Errorf("links of the crashed unit = %v", l)
	}

	detachedKey := strings.TrimPrefix(e2eLinks(t, cs, detached)[0], "jira:")
	e2eOK(t, cs, "link_remove", map[string]any{"id": detached, "kind": "external", "target": "jira:" + detachedKey})
	e2eRetitle(t, cs, detached, "Retitled after detaching")
	for i := 0; i < 2; i++ {
		j.srv.ResetRequests()
		j.mustRun(t, "tickets", "sync")
		if j.creates() != 0 {
			t.Fatalf("a detached unit got a second ticket")
		}
	}
	if got := j.issue(t, detachedKey); got.Title != "Detached slice" {
		t.Errorf("a detached ticket was changed: %+v", got)
	}
	if list := j.mustRun(t, "tickets").stdout; !strings.Contains(list, detached+"  "+detachedKey+"  ") || !strings.Contains(list, "(detached)") {
		t.Errorf("ralph tickets:\n%s", list)
	}
	if l := e2eLinks(t, cs, detached); len(l) != 0 {
		t.Errorf("the detached unit was linked again: %v", l)
	}
}

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func ticketRows(t *testing.T, dir string) int {
	t.Helper()
	d, err := sql.Open("sqlite", "file:"+filepath.Join(dir, ".ralph", "store.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	var n int
	if err := d.QueryRow("SELECT count(*) FROM tickets").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestE2ETicketsSettingsAreUserOnly(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	srv := jiratest.NewServer(t, jiratest.Options{Project: "ABC"})
	e.env = append(e.env, "JIRA_TOKEN="+srv.Token())
	appendFile(t, filepath.Join(e.dir, ".ralph", "project.yaml"),
		"taskmanager:\n  provider: jira\njira:\n  site: "+srv.URL()+"\n  user: "+srv.User()+"\n  token: env:JIRA_TOKEN\n  project: ABC\n")

	r := e.run(t, "status")
	if r.code != 0 {
		t.Fatalf("status: exit %d: %s", r.code, r.stderr)
	}
	for _, path := range []string{"jira.site", "jira.user", "jira.token"} {
		if want := `"` + path + `" is user-only and was ignored in the project file`; !strings.Contains(r.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.stderr)
		}
	}
	if strings.Contains(r.stderr, `"jira.project"`) || strings.Contains(r.stderr, `"taskmanager.provider"`) {
		t.Errorf("a setting that may be committed was ignored:\n%s", r.stderr)
	}

	cs := e.connect(t, io.Discard)
	b := e2eCreate(t, cs, "brief", "", "Ship it", "Why.")
	s := e2eCreate(t, cs, "slice", b, "The slice", "What.")
	doc := e2eOK(t, cs, "doc_create", map[string]any{"kind": "design", "title": "Design", "body": "How."})["id"].(string)
	r = e.run(t, "docs", "link", doc, s)
	if r.code != 0 || strings.Contains(r.stderr, "ticket") || strings.Contains(r.stderr, "failed") {
		t.Errorf("an automatic run was not quiet: exit %d stderr %q", r.code, r.stderr)
	}
	r = e.run(t, "tickets")
	if r.code == 0 || !strings.Contains(r.stderr, "jira.site") {
		t.Errorf("ralph tickets: exit %d stderr %q", r.code, r.stderr)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("%d requests reached the site named in the project file", n)
	}
	if n := ticketRows(t, e.dir); n != 0 {
		t.Errorf("%d ticket rows", n)
	}
}

func TestE2ETicketsProviderNoneTouchesNothing(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	srv := jiratest.NewServer(t, jiratest.Options{Project: "ABC"})
	e.env = append(e.env, "JIRA_TOKEN="+srv.Token())
	e.writeUserConfig(t, "jira:\n  site: "+srv.URL()+"\n  user: "+srv.User()+"\n  project: ABC\n")

	cs := e.connect(t, io.Discard)
	b := e2eCreate(t, cs, "brief", "", "Ship it", "Why.")
	s := e2eCreate(t, cs, "slice", b, "The slice", "What.")
	e2eCreate(t, cs, "task", s, "The task", "How.")
	e2eSetState(t, cs, "T-1", "TODO")
	doc := e2eOK(t, cs, "doc_create", map[string]any{"kind": "design", "title": "Design", "body": "How."})["id"].(string)
	if n := noticeCount(e2eCall(t, cs, "status", nil), "tickets_need_attention"); n != 0 {
		t.Errorf("a tickets notice with no provider")
	}
	for _, args := range [][]string{{"docs", "link", doc, s}, {"status"}, {"status", "--all"}, {"monitor", "--no-poll"}, {"docs", "unlink", doc, s}} {
		if r := e.run(t, args...); r.code != 0 {
			t.Fatalf("%v: exit %d: %s", args, r.code, r.stderr)
		} else if strings.Contains(r.stdout, "tickets:") {
			t.Errorf("%v shows a tickets line:\n%s", args, r.stdout)
		}
	}
	if r := e.run(t, "tickets", "sync"); r.code == 0 {
		t.Errorf("tickets sync worked with no provider")
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("%d requests", n)
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".ralph", "tickets.lock")); err == nil {
		t.Errorf("tickets.lock exists")
	}
	if n := ticketRows(t, e.dir); n != 0 {
		t.Errorf("%d ticket rows", n)
	}
	if links := e2eLinks(t, cs, s); len(links) != 0 {
		t.Errorf("links = %v", links)
	}
}

func TestE2ETicketsMigration(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	var m db.Migration
	for _, c := range db.Migrations {
		if c.Name == "create tickets" {
			m = c
		}
	}
	if m.Version == 0 {
		t.Fatal("no create tickets migration")
	}
	store := filepath.Join(e.dir, ".ralph", "store.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(store + suffix)
	}
	old, err := db.Open(context.Background(), store, db.Options{Migrations: db.Migrations[:m.Version-1], Mode: db.MigrateAuto})
	if err != nil {
		t.Fatal(err)
	}
	_ = old.Close()

	r := e.run(t, "migrate")
	if r.code != 0 {
		t.Fatalf("migrate: exit %d: %s", r.code, r.stderr)
	}
	backups, _ := filepath.Glob(filepath.Join(e.dir, ".ralph", "backups", fmt.Sprintf("pre-migrate-v%d-*.db", m.Version-1)))
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	d, err := sql.Open("sqlite", "file:"+store+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	for _, table := range []string{"tickets", "ticket_state"} {
		var n int
		if err := d.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s: %d, %v", table, n, err)
		}
	}
	var v int
	if err := d.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != len(db.Migrations) {
		t.Errorf("user_version = %d, %v; want %d", v, err, len(db.Migrations))
	}
	bk, err := sql.Open("sqlite", "file:"+backups[0]+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bk.Close() }()
	var n int
	if err := bk.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = 'tickets'").Scan(&n); err != nil || n != 0 {
		t.Errorf("the backup already has the ticket tables: %d, %v", n, err)
	}
	if r := e.run(t, "status"); r.code != 0 {
		t.Errorf("status after migrating: %s", r.stderr)
	}
}
