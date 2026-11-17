package jira

import (
	"errors"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/taskmanager/jira/jiratest"
)

var flow4 = []string{"To Do", "In Progress", "In Review", "Done"}

func chain(t *testing.T) (*jiratest.Server, *Client, string) {
	t.Helper()
	srv, c := newStub(t, jiratest.Options{})
	srv.SetWorkflow("To Do", map[string][]string{
		"To Do":       {"In Progress"},
		"In Progress": {"To Do", "In Review"},
		"In Review":   {"In Progress", "Done"},
		"Done":        {"In Review"},
	})
	return srv, c, srv.Seed(jiratest.Issue{Summary: "t"})
}

func transitionPosts(srv *jiratest.Server, key string) []jiratest.Request {
	return requestsTo(srv, "POST", "/rest/api/3/issue/"+key+"/transitions")
}

func TestMoveAlreadyThereMakesNoTransitionRequest(t *testing.T) {
	srv, c, key := chain(t)
	srv.ResetRequests()
	m, err := c.Move(ctx, key, " to do ", flow4)
	if err != nil || m.From != "To Do" || m.To != " to do " || len(m.Via) != 0 {
		t.Fatalf("Move = %+v, %v", m, err)
	}
	if n := len(srv.Requests()); n != 1 {
		t.Errorf("%d requests, want only the status read", n)
	}
}

func TestMoveDirectTransition(t *testing.T) {
	srv, c, key := chain(t)
	m, err := c.Move(ctx, key, "In Progress", nil)
	if err != nil || m.From != "To Do" || m.To != "In Progress" || len(m.Via) != 0 {
		t.Fatalf("Move = %+v, %v", m, err)
	}
	posts := transitionPosts(srv, key)
	if len(posts) != 1 {
		t.Fatalf("%d transition requests", len(posts))
	}
	body, _ := posts[0].Body.(map[string]any)
	tr, _ := body["transition"].(map[string]any)
	if len(body) != 1 || len(tr) != 1 || tr["id"] == "" || tr["id"] == nil {
		t.Errorf("body = %v", posts[0].Body)
	}
	if srv.Status(key) != "In Progress" {
		t.Errorf("status = %s", srv.Status(key))
	}
}

func TestMoveCaseInsensitiveStatus(t *testing.T) {
	srv, c, key := chain(t)
	m, err := c.Move(ctx, key, "  in progress ", nil)
	if err != nil || m.To != "In Progress" {
		t.Fatalf("Move = %+v, %v", m, err)
	}
	if srv.Status(key) != "In Progress" {
		t.Errorf("status = %s", srv.Status(key))
	}
}

func TestMoveThroughFlowForward(t *testing.T) {
	srv, c, key := chain(t)
	m, err := c.Move(ctx, key, "Done", flow4)
	want := taskmanager.Move{From: "To Do", To: "Done", Via: []string{"In Progress", "In Review"}}
	if err != nil || !reflect.DeepEqual(m, want) {
		t.Fatalf("Move = %+v, %v", m, err)
	}
	if n := len(transitionPosts(srv, key)); n != 3 {
		t.Errorf("%d transitions, want 3", n)
	}
	if srv.Status(key) != "Done" {
		t.Errorf("status = %s", srv.Status(key))
	}
}

func TestMoveThroughFlowBackward(t *testing.T) {
	srv, c, _ := chain(t)
	done := srv.Seed(jiratest.Issue{Summary: "d", Status: "Done"})
	m, err := c.Move(ctx, done, "To Do", flow4)
	want := taskmanager.Move{From: "Done", To: "To Do", Via: []string{"In Review", "In Progress"}}
	if err != nil || !reflect.DeepEqual(m, want) {
		t.Fatalf("Move = %+v, %v", m, err)
	}
}

func TestMoveDirectPreferredOverFlow(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	srv.SetWorkflow("To Do", map[string][]string{"To Do": {"In Progress", "Done"}, "In Progress": {"Done"}})
	key := srv.Seed(jiratest.Issue{})
	m, err := c.Move(ctx, key, "Done", flow4)
	if err != nil || m.To != "Done" || len(m.Via) != 0 {
		t.Fatalf("Move = %+v, %v", m, err)
	}
	if n := len(transitionPosts(srv, key)); n != 1 {
		t.Errorf("%d transitions, want 1", n)
	}
}

func TestMoveStopsAtMissingStep(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	srv.SetWorkflow("To Do", map[string][]string{"To Do": {"In Progress"}, "In Progress": {"Blocked", "To Do"}})
	key := srv.Seed(jiratest.Issue{})
	m, err := c.Move(ctx, key, "Done", flow4)
	want := "cannot move " + key + " from To Do to Done: no transition from In Progress to In Review; available: Blocked, To Do"
	if !errors.Is(err, taskmanager.ErrInvalid) || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if m.From != "To Do" || !slices.Equal(m.Via, []string{"In Progress"}) {
		t.Errorf("Move = %+v", m)
	}
	if srv.Status(key) != "In Progress" {
		t.Errorf("status = %s", srv.Status(key))
	}
}

func TestMoveNoFlowNoDirectTransition(t *testing.T) {
	srv, c, key := chain(t)
	_, err := c.Move(ctx, key, "Done", nil)
	want := "cannot move " + key + " from To Do to Done: no transition leads there; available: In Progress"
	if !errors.Is(err, taskmanager.ErrInvalid) || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if n := len(transitionPosts(srv, key)); n != 0 {
		t.Errorf("%d transitions", n)
	}
}

func TestMoveStatusNotInFlow(t *testing.T) {
	_, c, key := chain(t)
	for name, fl := range map[string][]string{"target": {"To Do", "In Progress"}, "current": {"In Review", "Done"}, "empty": nil} {
		_, err := c.Move(ctx, key, "Done", fl)
		want := "cannot move " + key + " from To Do to Done: no transition leads there; available: In Progress"
		if !errors.Is(err, taskmanager.ErrInvalid) || err.Error() != want {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestMoveRequiredFieldsMessage(t *testing.T) {
	srv, c, key := chain(t)
	srv.RequireFieldsFor("To Do", "In Progress")
	m, err := c.Move(ctx, key, "In Progress", nil)
	want := "transition to In Progress needs more than ralph can give: resolution: Field 'resolution' is required"
	if !errors.Is(err, taskmanager.ErrInvalid) || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if len(m.Via) != 0 || srv.Status(key) != "To Do" {
		t.Errorf("Move = %+v, status %s", m, srv.Status(key))
	}
}

func TestMoveTooLongFlow(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	var fl []string
	trs := map[string][]string{}
	for i := 0; i <= 22; i++ {
		fl = append(fl, "S"+string(rune('a'+i)))
		if i > 0 {
			trs[fl[i-1]] = []string{fl[i]}
		}
	}
	srv.SetWorkflow(fl[0], trs)
	key := srv.Seed(jiratest.Issue{})
	_, err := c.Move(ctx, key, fl[22], fl)
	if !errors.Is(err, taskmanager.ErrInvalid) || err.Error() != "cannot move "+key+" from Sa to Sw: flow is too long" {
		t.Errorf("err = %v", err)
	}
	if n := len(transitionPosts(srv, key)); n != 0 {
		t.Errorf("%d transitions", n)
	}
	if _, err := c.Move(ctx, key, fl[20], fl); err != nil {
		t.Errorf("a flow of exactly the limit failed: %v", err)
	}
}

func TestMoveNotFound(t *testing.T) {
	_, c, _ := chain(t)
	if _, err := c.Move(ctx, "RAL-999", "Done", flow4); !errors.Is(err, taskmanager.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestMoveRateLimitedMidWay(t *testing.T) {
	srv, c, key := chain(t)
	limited := false
	c.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" && limited {
			srv.RateLimit(time.Minute)
		}
		if r.Method == "POST" {
			limited = true
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	m, err := c.Move(ctx, key, "Done", flow4)
	if !errors.Is(err, taskmanager.ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(m.Via, []string{"In Progress"}) || m.From != "To Do" {
		t.Errorf("Move = %+v", m)
	}
	srv.Heal()
	if srv.Status(key) != "In Progress" {
		t.Errorf("status = %s", srv.Status(key))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
