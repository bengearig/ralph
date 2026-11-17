package jira

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/taskmanager/jira/jiratest"
)

func boardLookups(srv *jiratest.Server) int {
	return len(requestsTo(srv, "GET", "/rest/agile/1.0/board"))
}

func TestScheduleUsesGivenBoard(t *testing.T) {
	srv := jiratest.NewServer(t, jiratest.Options{})
	srv.AddBoard(3, "A", "RAL")
	srv.AddBoard(4, "B", "RAL")
	srv.AddSprint(4, 40, "Sprint 1", "active")
	c, err := New(Config{Site: srv.URL(), User: srv.User(), Project: "RAL", Board: 4, Token: func() (string, error) { return srv.Token(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	key := srv.Seed(jiratest.Issue{})
	if err := c.Schedule(ctx, key, "Sprint 1"); err != nil {
		t.Fatal(err)
	}
	if boardLookups(srv) != 0 {
		t.Error("the board was looked up")
	}
	if got := srv.SprintIssues(40); !slices.Equal(got, []string{key}) {
		t.Errorf("sprint issues = %v", got)
	}
}

func TestScheduleOnlyBoard(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	srv.AddBoard(3, "A", "RAL")
	srv.AddBoard(9, "Other", "OTH")
	srv.AddSprint(3, 30, "Sprint 1", "future")
	key := srv.Seed(jiratest.Issue{})
	if err := c.Schedule(ctx, key, "Sprint 1"); err != nil {
		t.Fatal(err)
	}
	reqs := requestsTo(srv, "GET", "/rest/agile/1.0/board")
	if len(reqs) != 1 || reqs[0].Query.Get("projectKeyOrId") != "RAL" || reqs[0].Query.Get("type") != "scrum" {
		t.Errorf("board requests = %+v", reqs)
	}
	sp := requestsTo(srv, "GET", "/rest/agile/1.0/board/3/sprint")
	if len(sp) != 1 || sp[0].Query.Get("state") != "active,future" || sp[0].Query.Get("maxResults") != "50" || sp[0].Query.Get("startAt") != "0" {
		t.Errorf("sprint requests = %+v", sp)
	}
}

func TestScheduleNoScrumBoard(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	err := c.Schedule(ctx, srv.Seed(jiratest.Issue{}), "Sprint 1")
	if !errors.Is(err, taskmanager.ErrInvalid) || err.Error() != "project RAL has no scrum board, so there are no sprints; unset jira.sprint" {
		t.Errorf("err = %v", err)
	}
}

func TestScheduleSeveralBoards(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	srv.AddBoard(8, "Beta", "RAL")
	srv.AddBoard(2, "Alpha", "RAL")
	err := c.Schedule(ctx, srv.Seed(jiratest.Issue{}), "Sprint 1")
	if !errors.Is(err, taskmanager.ErrInvalid) || err.Error() != "project RAL has several scrum boards (2: Alpha, 8: Beta); set jira.board" {
		t.Errorf("err = %v", err)
	}
}

func TestScheduleFindsSprintOnSecondPage(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{PageSize: 2})
	srv.AddBoard(1, "B", "RAL")
	for i := 1; i <= 4; i++ {
		srv.AddSprint(1, i, "Other "+string(rune('0'+i)), "future")
	}
	srv.AddSprint(1, 5, "Target", "future")
	key := srv.Seed(jiratest.Issue{})
	if err := c.Schedule(ctx, key, "Target"); err != nil {
		t.Fatal(err)
	}
	if got := srv.SprintIssues(5); !slices.Equal(got, []string{key}) {
		t.Errorf("sprint issues = %v", got)
	}
	var starts []string
	for _, r := range requestsTo(srv, "GET", "/rest/agile/1.0/board/1/sprint") {
		starts = append(starts, r.Query.Get("startAt"))
	}
	if !reflect.DeepEqual(starts, []string{"0", "2", "4"}) {
		t.Errorf("startAt = %v", starts)
	}
}

func TestScheduleActiveWinsOverFuture(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	srv.AddBoard(1, "B", "RAL")
	srv.AddSprint(1, 1, "Same", "future")
	srv.AddSprint(1, 2, "Same", "active")
	srv.AddSprint(1, 3, "Same", "future")
	key := srv.Seed(jiratest.Issue{})
	if err := c.Schedule(ctx, key, "Same"); err != nil {
		t.Fatal(err)
	}
	if got := srv.SprintIssues(2); !slices.Equal(got, []string{key}) || len(srv.SprintIssues(1)) != 0 {
		t.Errorf("sprint issues = %v", got)
	}
}

func TestScheduleUnknownSprintNotFound(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	srv.AddBoard(1, "B", "RAL")
	srv.AddSprint(1, 1, "sprint 1", "active")
	srv.AddSprint(1, 2, "Sprint 1", "closed")
	err := c.Schedule(ctx, srv.Seed(jiratest.Issue{}), "Sprint 1")
	if !errors.Is(err, taskmanager.ErrNotFound) || err.Error() != `sprint "Sprint 1" was not found among the active and future sprints of board 1` {
		t.Errorf("err = %v", err)
	}
}

func TestScheduleCachesBoardAndSprint(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	srv.AddBoard(1, "B", "RAL")
	srv.AddSprint(1, 1, "Sprint 1", "active")
	a, b := srv.Seed(jiratest.Issue{}), srv.Seed(jiratest.Issue{})
	if err := c.Schedule(ctx, a, "Sprint 1"); err != nil {
		t.Fatal(err)
	}
	srv.ResetRequests()
	if err := c.Schedule(ctx, b, "Sprint 1"); err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].Method != "POST" {
		t.Errorf("requests = %+v", reqs)
	}
}

func TestScheduleClosedSprintDropsCache(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	srv.AddBoard(1, "B", "RAL")
	srv.AddSprint(1, 1, "Sprint 1", "active")
	a, b := srv.Seed(jiratest.Issue{}), srv.Seed(jiratest.Issue{})
	if err := c.Schedule(ctx, a, "Sprint 1"); err != nil {
		t.Fatal(err)
	}
	srv.SetSprintState(1, "closed")
	srv.AddSprint(1, 2, "Sprint 1", "active")
	err := c.Schedule(ctx, b, "Sprint 1")
	if !errors.Is(err, taskmanager.ErrInvalid) || err.Error() != "invalid request: Cannot add issues to a closed sprint." {
		t.Fatalf("err = %v", err)
	}
	srv.ResetRequests()
	if err := c.Schedule(ctx, b, "Sprint 1"); err != nil {
		t.Fatal(err)
	}
	if got := srv.SprintIssues(2); !slices.Equal(got, []string{b}) {
		t.Errorf("sprint issues = %v", got)
	}
	if len(requestsTo(srv, "GET", "/rest/agile/1.0/board/1/sprint")) != 1 {
		t.Error("the sprint was not resolved again")
	}
}

func TestSchedulePostBody(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	srv.AddBoard(1, "B", "RAL")
	srv.AddSprint(1, 6, "Sprint 1", "active")
	key := srv.Seed(jiratest.Issue{})
	if err := c.Schedule(ctx, key, "Sprint 1"); err != nil {
		t.Fatal(err)
	}
	posts := requestsTo(srv, "POST", "/rest/agile/1.0/sprint/6/issue")
	want := map[string]any{"issues": []any{key}}
	if len(posts) != 1 || !reflect.DeepEqual(posts[0].Body, want) {
		t.Errorf("posts = %+v", posts)
	}
}

func TestScheduleTicketNotFound(t *testing.T) {
	srv, c := newStub(t, jiratest.Options{})
	srv.AddBoard(1, "B", "RAL")
	srv.AddSprint(1, 1, "Sprint 1", "active")
	if err := c.Schedule(ctx, "RAL-404", "Sprint 1"); !errors.Is(err, taskmanager.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
