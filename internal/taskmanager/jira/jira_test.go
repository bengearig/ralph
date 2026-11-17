package jira

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/adf"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/taskmanager/jira/jiratest"
)

func TestCheckAccountAndProject(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	acc, err := c.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if acc.Name != "Ralph Tester" || acc.Email != srv.User() {
		t.Errorf("account = %+v", acc)
	}
	if len(requestsTo(srv, "GET", "/rest/api/3/myself")) != 1 || len(requestsTo(srv, "GET", "/rest/api/3/project/RAL")) != 1 {
		t.Errorf("requests = %+v", srv.Requests())
	}
}

func TestCheckProjectNotFoundMessage(t *testing.T) {
	srv := jiratest.NewServer(t, jiratest.Options{Project: "RAL"})
	c, err := New(Config{Site: srv.URL(), User: srv.User(), Project: "NOPE", Token: func() (string, error) { return srv.Token(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Check(ctx)
	if !errors.Is(err, taskmanager.ErrNotFound) || err.Error() != "project NOPE was not found or is not visible to this account" {
		t.Errorf("err = %v", err)
	}
}

func TestTypesFromProject(t *testing.T) {
	_, c := newStub(t, jiratest.Options{Types: []string{"Epic", "Task"}})
	got, err := c.Types(ctx)
	if err != nil || !reflect.DeepEqual(got, []string{"Epic", "Task"}) {
		t.Errorf("Types = %v, %v", got, err)
	}
}

func TestCreateSendsFieldsAndLabels(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	if _, err := c.Create(ctx, taskmanager.New{Type: "Task", Title: "A title", Body: "Some **bold** text", Ref: "ralph-p-1"}); err != nil {
		t.Fatal(err)
	}
	reqs := requestsTo(srv, "POST", "/rest/api/3/issue")
	if len(reqs) != 1 {
		t.Fatalf("requests = %+v", srv.Requests())
	}
	want := roundTrip(t, map[string]any{"fields": map[string]any{
		"project":     map[string]any{"key": "RAL"},
		"issuetype":   map[string]any{"name": "Task"},
		"summary":     "A title",
		"description": adf.FromMarkdown("Some **bold** text"),
		"labels":      []string{"ralph", "ralph-p-1"},
	}})
	if !reflect.DeepEqual(reqs[0].Body, want) {
		t.Errorf("body = %v\nwant %v", reqs[0].Body, want)
	}
}

func TestCreateWithParent(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	parent := srv.Seed(jiratest.Issue{Type: "Feature", Summary: "p"})
	tk, err := c.Create(ctx, taskmanager.New{Type: "Task", Title: "child", Parent: parent, Ref: "ralph-c"})
	if err != nil || tk.Parent != parent {
		t.Fatalf("Create = %+v, %v", tk, err)
	}
	fields := requestsTo(srv, "POST", "/rest/api/3/issue")[0].Body.(map[string]any)["fields"].(map[string]any)
	if !reflect.DeepEqual(fields["parent"], map[string]any{"key": parent}) {
		t.Errorf("parent = %v", fields["parent"])
	}
}

func TestCreateBlankBodyOmitsDescription(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	for _, b := range []string{"", " \n\t "} {
		if _, err := c.Create(ctx, taskmanager.New{Type: "Task", Title: "t", Body: b}); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range requestsTo(srv, "POST", "/rest/api/3/issue") {
		if _, ok := r.Body.(map[string]any)["fields"].(map[string]any)["description"]; ok {
			t.Errorf("body = %v, want no description", r.Body)
		}
	}
}

func TestCreateBodyLimited(t *testing.T) {
	_, c := newStub(t, jiratest.Options{})
	body := strings.Repeat("a line of text\n\n", 3000)
	if len([]rune(body)) < 40000 {
		t.Fatal("the body is too short")
	}
	tk, err := c.Create(ctx, taskmanager.New{Type: "Task", Title: "t", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(tk.Body)); n > 30000 || !strings.Contains(tk.Body, "[truncated") {
		t.Errorf("body has %d characters: ...%q", n, tk.Body[max(0, len(tk.Body)-80):])
	}
}

func TestCreateInvalidTypeIsErrInvalid(t *testing.T) {
	_, c := newStub(t, jiratest.Options{})
	_, err := c.Create(ctx, taskmanager.New{Type: "Nope", Title: "t"})
	if !errors.Is(err, taskmanager.ErrInvalid) || !strings.Contains(err.Error(), "issuetype: specified issue type is not valid") {
		t.Errorf("err = %v", err)
	}
}

func TestCreateInvalidParentIsErrInvalid(t *testing.T) {
	_, c := newStub(t, jiratest.Options{})
	_, err := c.Create(ctx, taskmanager.New{Type: "Task", Title: "t", Parent: "RAL-404"})
	if !errors.Is(err, taskmanager.ErrInvalid) || !strings.Contains(err.Error(), "parent: Given parent work item does not exist") {
		t.Errorf("err = %v", err)
	}
}

func TestCreateReturnsReadBackTicket(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	tk, err := c.Create(ctx, taskmanager.New{Type: "Task", Title: "T", Body: "Body", Ref: "ralph-p-9"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Key != "RAL-1" || tk.ID == "" || tk.Type != "Task" || tk.Title != "T" || tk.Body != "Body" || tk.Status != "To Do" || tk.Ref != "ralph-p-9" || tk.Parent != "" {
		t.Errorf("ticket = %+v", tk)
	}
	reads := requestsTo(srv, "GET", "/rest/api/3/issue/RAL-1")
	if len(reads) != 1 || reads[0].Query.Get("fields") != "summary,description,status,parent,labels,issuetype,updated" {
		t.Errorf("reads = %+v", reads)
	}
}

func TestFindNoneNilOneTicketTwoLowestKey(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	got, err := c.Find(ctx, "ralph-p-1")
	if got != nil || err != nil {
		t.Fatalf("Find = %v, %v", got, err)
	}
	srv.Seed(jiratest.Issue{Key: "RAL-10", Summary: "ten", Labels: []string{"ralph", "ralph-p-1"}})
	got, err = c.Find(ctx, "ralph-p-1")
	if err != nil || got == nil || got.Key != "RAL-10" || got.Ref != "ralph-p-1" || got.Title != "ten" {
		t.Fatalf("Find = %+v, %v", got, err)
	}
	srv.Seed(jiratest.Issue{Key: "RAL-9", Summary: "nine", Labels: []string{"ralph", "ralph-p-1"}})
	got, err = c.Find(ctx, "ralph-p-1")
	if err != nil || got == nil || got.Key != "RAL-9" {
		t.Fatalf("Find = %+v, %v, want RAL-9 (numeric, not text, order)", got, err)
	}
	last := srv.Requests()[len(srv.Requests())-1]
	if last.Body.(map[string]any)["maxResults"] != float64(2) {
		t.Errorf("body = %v", last.Body)
	}
}

func TestFindQuotesRefInJQL(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	ref := `a"b\c`
	srv.Seed(jiratest.Issue{Summary: "odd", Labels: []string{ref}})
	got, err := c.Find(ctx, ref)
	if err != nil || got == nil || got.Title != "odd" {
		t.Fatalf("Find = %+v, %v", got, err)
	}
	req := requestsTo(srv, "POST", "/rest/api/3/search/jql")[0]
	want := `project = "RAL" AND labels = "a\"b\\c"`
	if req.Body.(map[string]any)["jql"] != want {
		t.Errorf("jql = %v, want %s", req.Body.(map[string]any)["jql"], want)
	}
	if !reflect.DeepEqual(req.Body.(map[string]any)["fields"], roundTrip(t, issueFields)) {
		t.Errorf("fields = %v", req.Body.(map[string]any)["fields"])
	}
}

func TestUpdateSendsTitleAndBody(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	key := srv.Seed(jiratest.Issue{Summary: "old", Body: "old body"})
	title := "line one\nline two " + strings.Repeat("é", 400)
	if err := c.Update(ctx, key, taskmanager.Fields{Title: title, Body: "new body"}); err != nil {
		t.Fatal(err)
	}
	fields := requestsTo(srv, "PUT", "/rest/api/3/issue/"+key)[0].Body.(map[string]any)["fields"].(map[string]any)
	summary := fields["summary"].(string)
	if strings.Contains(summary, "\n") || len([]rune(summary)) != 255 || !strings.HasPrefix(summary, "line one line two é") {
		t.Errorf("summary = %q", summary)
	}
	if !reflect.DeepEqual(fields["description"], roundTrip(t, adf.FromMarkdown("new body"))) {
		t.Errorf("description = %v", fields["description"])
	}

	if err := c.Update(ctx, key, taskmanager.Fields{Title: "t", Body: " \n"}); err != nil {
		t.Fatal(err)
	}
	reqs := requestsTo(srv, "PUT", "/rest/api/3/issue/"+key)
	fields = reqs[1].Body.(map[string]any)["fields"].(map[string]any)
	if v, ok := fields["description"]; !ok || v != nil {
		t.Errorf("description = %v (present %v), want an explicit null", v, ok)
	}
	got, _ := c.Get(ctx, []string{key})
	if got[key].Body != "" || got[key].Title != "t" {
		t.Errorf("ticket = %+v", got[key])
	}
}

func TestUpdateNotFound(t *testing.T) {
	_, c := newStub(t, jiratest.Options{})
	if err := c.Update(ctx, "RAL-404", taskmanager.Fields{Title: "t"}); !errors.Is(err, taskmanager.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func seedMany(srv *jiratest.Server, n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = srv.Seed(jiratest.Issue{Summary: fmt.Sprintf("issue %d", i)})
	}
	return keys
}

func searches(srv *jiratest.Server) []map[string]any {
	var out []map[string]any
	for _, r := range requestsTo(srv, "POST", "/rest/api/3/search/jql") {
		out = append(out, r.Body.(map[string]any))
	}
	return out
}

func TestGetBatchesOfFifty(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	keys := seedMany(srv, 120)
	got, err := c.Get(ctx, keys)
	if err != nil || len(got) != 120 {
		t.Fatalf("Get = %d tickets, %v", len(got), err)
	}
	var sizes []int
	for _, s := range searches(srv) {
		sizes = append(sizes, strings.Count(s["jql"].(string), `"`)/2)
	}
	if !reflect.DeepEqual(sizes, []int{50, 50, 20}) {
		t.Errorf("batch sizes = %v", sizes)
	}
	if !strings.HasPrefix(searches(srv)[0]["jql"].(string), `key in ("RAL-1","RAL-2"`) {
		t.Errorf("jql = %v", searches(srv)[0]["jql"])
	}
}

func TestGetFollowsNextPageToken(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{PageSize: 2})
	keys := seedMany(srv, 5)
	got, err := c.Get(ctx, keys)
	if err != nil || len(got) != 5 {
		t.Fatalf("Get = %d tickets, %v", len(got), err)
	}
	ss := searches(srv)
	if len(ss) != 3 || ss[0]["nextPageToken"] != nil || ss[1]["nextPageToken"] == nil || ss[2]["nextPageToken"] == nil {
		t.Errorf("searches = %v", ss)
	}
	if n := len(requestsTo(srv, "GET", "/rest/api/3/issue/"+keys[0])); n != 0 {
		t.Errorf("%d direct reads, want none", n)
	}
}

func TestGetPageCap(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{PageSize: 1})
	keys := seedMany(srv, 25)
	got, err := c.Get(ctx, keys)
	if err != nil || len(got) != 25 {
		t.Fatalf("Get = %d tickets, %v", len(got), err)
	}
	if n := len(searches(srv)); n != 20 {
		t.Errorf("%d search pages, want 20", n)
	}
	direct := 0
	for _, k := range keys {
		direct += len(requestsTo(srv, "GET", "/rest/api/3/issue/"+k))
	}
	if direct != 5 {
		t.Errorf("%d direct reads, want the 5 keys past the cap", direct)
	}
}

func TestGetMissingKeyAbsent(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	keys := seedMany(srv, 2)
	srv.Delete(keys[1])
	got, err := c.Get(ctx, []string{keys[0], keys[1], "RAL-777"})
	if err != nil || len(got) != 1 || got[keys[0]].Title != "issue 0" {
		t.Errorf("Get = %+v, %v", got, err)
	}
}

func TestGetSearchRefusalFallsBackToReads(t *testing.T) {
	var reads []string
	c := rawClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"errorMessages":["An issue with key 'RAL-2' does not exist for field 'key'."],"errors":{}}`)
			return
		}
		reads = append(reads, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "RAL-2") {
			w.WriteHeader(404)
			return
		}
		_, _ = io.WriteString(w, `{"id":"1","key":"RAL-1","fields":{"summary":"one","status":{"name":"To Do"},"issuetype":{"name":"Task"}}}`)
	}, nil)
	got, err := c.Get(ctx, []string{"RAL-1", "RAL-2"})
	if err != nil || len(got) != 1 || got["RAL-1"].Title != "one" || len(reads) != 2 {
		t.Errorf("Get = %+v, %v; reads = %v", got, err, reads)
	}
}

func TestGetMovedKeyReturnedWithNewKey(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	keys := seedMany(srv, 2)
	srv.MoveKey(keys[0], "NEW-5")
	got, err := c.Get(ctx, keys)
	if err != nil || len(got) != 2 {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if got[keys[0]].Key != "NEW-5" || got[keys[0]].Title != "issue 0" || got[keys[1]].Key != keys[1] {
		t.Errorf("Get = %+v", got)
	}
}

func TestGetConvertsAdfBody(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	key := srv.Seed(jiratest.Issue{Summary: "rich", Description: json.RawMessage(`{"version":1,"type":"doc","content":[
	  {"type":"panel","attrs":{"panelType":"info"},"content":[{"type":"paragraph","content":[{"type":"text","text":"Heads up"}]}]},
	  {"type":"table","content":[{"type":"tableRow","content":[
	    {"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Cell A"}]}]},
	    {"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Cell B"}]}]}]}]},
	  {"type":"paragraph","content":[{"type":"mention","attrs":{"id":"123","text":"@Jo Bloggs"}},{"type":"text","text":" please look"}]}]}`)})
	got, err := c.Get(ctx, []string{key})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Heads up", "Cell A", "Cell B", "@Jo Bloggs", "please look"} {
		if !strings.Contains(got[key].Body, want) {
			t.Errorf("body = %q, want it to contain %q", got[key].Body, want)
		}
	}
}

func TestGetInvalidDescriptionIsEmptyBody(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	srv, c := newStub(t, jiratest.Options{})
	bad := srv.Seed(jiratest.Issue{Summary: "bad", Description: json.RawMessage(`{"type":"paragraph"}`)})
	text := srv.Seed(jiratest.Issue{Summary: "text", Description: json.RawMessage(`"plain text"`)})
	none := srv.Seed(jiratest.Issue{Summary: "none"})
	got, err := c.Get(ctx, []string{bad, text, none})
	if err != nil {
		t.Fatal(err)
	}
	if got[bad].Body != "" || got[text].Body != "" || got[none].Body != "" {
		t.Errorf("bodies = %q %q %q", got[bad].Body, got[text].Body, got[none].Body)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), bad) {
		t.Errorf("log = %q", logs.String())
	}
}

func TestGetEmptyListNoRequest(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	for _, keys := range [][]string{nil, {}} {
		got, err := c.Get(ctx, keys)
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("Get = %v, %v", got, err)
		}
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("%d requests", n)
	}
}

func TestGetMapsFields(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	parent := srv.Seed(jiratest.Issue{Type: "Feature", Summary: "p"})
	key := srv.Seed(jiratest.Issue{Type: "Task", Summary: "S", Body: "B", Status: "In Progress", Parent: parent, Labels: []string{"ralph", "other", "ralph-p-3"}})
	got, err := c.Get(ctx, []string{key})
	if err != nil {
		t.Fatal(err)
	}
	tk := got[key]
	if tk.Key != key || tk.ID == "" || tk.Type != "Task" || tk.Title != "S" || tk.Body != "B" || tk.Status != "In Progress" || tk.Parent != parent || tk.Ref != "ralph-p-3" {
		t.Errorf("ticket = %+v", tk)
	}
}

func TestNormalizeTitle(t *testing.T) {
	_, c := newStub(t, jiratest.Options{})
	for _, tc := range []struct{ in, want string }{
		{"a\nb", "a b"},
		{"a\r\nb\rc", "a b c"},
		{"  padded \n", "padded"},
		{strings.Repeat("é", 300), strings.Repeat("é", 255)},
		{strings.Repeat("a", 254) + " b", strings.Repeat("a", 254)},
	} {
		if got := c.Normalize(taskmanager.Fields{Title: tc.in}).Title; got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeBodyMatchesAdfNormalize(t *testing.T) {
	_, c := newStub(t, jiratest.Options{})
	for _, b := range []string{"", "text\n\n  \n", "- a\n- b  \n\n**x**", strings.Repeat("line\n\n", 9000)} {
		want := adf.Normalize(adf.Limit(b, 30000))
		if got := c.Normalize(taskmanager.Fields{Body: b}).Body; got != want {
			t.Errorf("Normalize body differs for %.20q", b)
		}
	}
}
