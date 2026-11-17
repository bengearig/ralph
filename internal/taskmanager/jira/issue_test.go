package jira

import (
	"errors"
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/taskmanager/jira/jiratest"
)

func noProject(t *testing.T) (*jiratest.Server, *Client) {
	t.Helper()
	srv := jiratest.NewServer(t, jiratest.Options{Project: "RAL"})
	c, err := New(Config{Site: srv.URL(), User: srv.User(), Token: func() (string, error) { return srv.Token(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	return srv, c
}

func TestProjectRequiredMethodsErrorClearly(t *testing.T) {
	srv, c := noProject(t)
	check := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, taskmanager.ErrInvalid) || err == nil || err.Error() != "jira.project is not set" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	_, err := c.Check(ctx)
	check("Check", err)
	_, err = c.Types(ctx)
	check("Types", err)
	_, err = c.Create(ctx, taskmanager.New{Type: "Task", Title: "t"})
	check("Create", err)
	_, err = c.Find(ctx, "ralph-x")
	check("Find", err)
	check("Schedule", c.Schedule(ctx, "RAL-1", "Sprint 1"))
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("%d requests made", n)
	}
}

func TestGetUpdateMoveWorkWithoutProject(t *testing.T) {
	srv, c := noProject(t)
	srv.SetWorkflow("To Do", map[string][]string{"To Do": {"Done"}, "Done": {}})
	key := srv.Seed(jiratest.Issue{Summary: "s", Body: "b"})
	got, err := c.Get(ctx, []string{key})
	if err != nil || got[key].Title != "s" {
		t.Fatalf("Get = %v, %v", got, err)
	}
	if err := c.Update(ctx, key, taskmanager.Fields{Title: "new", Body: "nb"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Move(ctx, key, "Done", nil); err != nil {
		t.Fatal(err)
	}
	if srv.Status(key) != "Done" {
		t.Errorf("status = %s", srv.Status(key))
	}
}

func TestIssueFound(t *testing.T) {
	srv, c := noProject(t)
	key := srv.Seed(jiratest.Issue{Summary: "Title", Body: "Body text", Parent: "RAL-1"})
	tk, err := c.Issue(ctx, key)
	if err != nil || tk.Key != key || tk.Title != "Title" || tk.Parent != "RAL-1" {
		t.Errorf("Issue = %+v, %v", tk, err)
	}
}

func TestIssueNotFoundMessage(t *testing.T) {
	_, c := noProject(t)
	_, err := c.Issue(ctx, "RAL-404")
	if !errors.Is(err, taskmanager.ErrNotFound) || err.Error() != "RAL-404 was not found in Jira or is not visible to this account" {
		t.Errorf("err = %v", err)
	}
}

func TestIssueReportsMovedKey(t *testing.T) {
	srv, c := noProject(t)
	key := srv.Seed(jiratest.Issue{Summary: "Moved"})
	srv.MoveKey(key, "NEW-7")
	tk, err := c.Issue(ctx, key)
	if err != nil || tk.Key != "NEW-7" || tk.Title != "Moved" {
		t.Errorf("Issue = %+v, %v", tk, err)
	}
}
