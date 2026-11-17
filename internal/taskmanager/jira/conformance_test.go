package jira

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/adf"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/taskmanager/jira/jiratest"
	"github.com/bengearig/ralph/internal/taskmanager/taskmanagertest"
)

func TestConformance(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{Types: []string{"Task", "Feature"}})
	srv.AddBoard(1, "Conformance board", srv.Project())
	srv.AddSprint(1, 10, "Conformance sprint", "active")
	taskmanagertest.Run(t, taskmanagertest.Subject{
		System: c,
		Controls: taskmanagertest.Controls{
			RejectCredential: srv.RejectCredentials,
			RateLimit:        func(until time.Time) { srv.RateLimit(time.Until(until)) },
			Down:             func() { srv.FailWith(http.StatusServiceUnavailable, `{"errorMessages":["Service Unavailable"]}`) },
			Heal:             srv.Heal,
			Delete:           srv.Delete,
			Edit:             srv.Edit,
		},
		Plan: "Conformance sprint",
	})
}

func TestJiraTestServerSpeaksDocumentedShapes(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{PageSize: 1})
	parent := srv.Seed(jiratest.Issue{Type: "Feature", Summary: "p", Labels: []string{"ralph", "ralph-a"}})
	child := srv.Seed(jiratest.Issue{Type: "Task", Summary: "c", Body: "text", Parent: parent, Labels: []string{"ralph", "ralph-b"}})
	srv.Seed(jiratest.Issue{Type: "Task", Summary: "d", Labels: []string{"ralph", "ralph-b"}})

	get := func(method, path string, body any) map[string]any {
		t.Helper()
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, srv.URL()+path, rd)
		req.SetBasicAuth(srv.User(), srv.Token())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}

	if me := get("GET", "/rest/api/3/myself", nil); me["displayName"] == nil || me["emailAddress"] != srv.User() {
		t.Errorf("myself = %v", me)
	}
	if p := get("GET", "/rest/api/3/project/RAL", nil); p["key"] != "RAL" || p["issueTypes"] == nil {
		t.Errorf("project = %v", p)
	}
	is := get("GET", "/rest/api/3/issue/"+child+"?fields=summary,description,parent", nil)
	f, _ := is["fields"].(map[string]any)
	if is["id"] == nil || is["key"] != child || f["summary"] != "c" || f["parent"].(map[string]any)["key"] != parent || f["status"] != nil {
		t.Errorf("issue = %v", is)
	}
	raw, _ := json.Marshal(f["description"])
	if doc, err := adf.ParseJSON(raw); err != nil || adf.ToMarkdown(doc) != "text" {
		t.Errorf("description = %s, %v", raw, err)
	}
	first := get("POST", "/rest/api/3/search/jql", map[string]any{"jql": `project = "RAL" AND labels = "ralph-b"`, "fields": []string{"summary"}, "maxResults": 5})
	if len(first["issues"].([]any)) != 1 || first["nextPageToken"] == nil || first["isLast"] != nil {
		t.Errorf("first page = %v", first)
	}
	second := get("POST", "/rest/api/3/search/jql", map[string]any{"jql": `project = "RAL" AND labels = "ralph-b"`, "nextPageToken": first["nextPageToken"]})
	if len(second["issues"].([]any)) != 1 || second["isLast"] != true || second["nextPageToken"] != nil {
		t.Errorf("second page = %v", second)
	}
	bad := get("POST", "/rest/api/3/issue", map[string]any{"fields": map[string]any{"project": map[string]any{"key": "RAL"}, "issuetype": map[string]any{"name": "Nope"}, "summary": "s"}})
	if errs, _ := bad["errors"].(map[string]any); errs["issuetype"] == nil || bad["errorMessages"] == nil {
		t.Errorf("error body = %v", bad)
	}
	trs := get("GET", "/rest/api/3/issue/"+child+"/transitions", nil)
	list, _ := trs["transitions"].([]any)
	if len(list) != 1 {
		t.Fatalf("transitions = %v", trs)
	}
	first0, _ := list[0].(map[string]any)
	to, _ := first0["to"].(map[string]any)
	if first0["id"] == nil || first0["name"] == nil || to["name"] != "In Progress" {
		t.Errorf("transition = %v", first0)
	}
	if bad := get("POST", "/rest/api/3/issue/"+child+"/transitions", map[string]any{"transition": map[string]any{"id": "nope"}}); bad["errorMessages"] == nil {
		t.Errorf("unknown transition = %v", bad)
	}
	srv.AddBoard(7, "Board", "RAL")
	srv.AddSprint(7, 70, "Sprint 1", "active")
	boards := get("GET", "/rest/agile/1.0/board?projectKeyOrId=RAL&type=scrum", nil)
	bl, _ := boards["values"].([]any)
	if len(bl) != 1 || boards["isLast"] != true || boards["startAt"] == nil || boards["maxResults"] == nil || bl[0].(map[string]any)["id"] != float64(7) {
		t.Errorf("boards = %v", boards)
	}
	sprints := get("GET", "/rest/agile/1.0/board/7/sprint?state=active,future", nil)
	sl, _ := sprints["values"].([]any)
	if len(sl) != 1 || sprints["isLast"] != true || sl[0].(map[string]any)["name"] != "Sprint 1" || sl[0].(map[string]any)["state"] != "active" {
		t.Errorf("sprints = %v", sprints)
	}
	if _, err := c.Create(ctx, taskmanager.New{Type: "Task", Title: "x"}); err != nil {
		t.Errorf("Create: %v", err)
	}
}
