package jira

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/taskmanager"
)

// TestLiveJira creates one ticket in a real Jira Cloud project, changes only
// that ticket and never touches an existing one.
func TestLiveJira(t *testing.T) {
	if os.Getenv("RALPH_JIRA_LIVE") != "1" {
		t.Skip("set RALPH_JIRA_LIVE=1, RALPH_JIRA_SITE, RALPH_JIRA_USER, RALPH_JIRA_TOKEN and RALPH_JIRA_PROJECT to run against a real Jira site")
	}
	site, user, token, project := os.Getenv("RALPH_JIRA_SITE"), os.Getenv("RALPH_JIRA_USER"), os.Getenv("RALPH_JIRA_TOKEN"), os.Getenv("RALPH_JIRA_PROJECT")
	for name, v := range map[string]string{"RALPH_JIRA_SITE": site, "RALPH_JIRA_USER": user, "RALPH_JIRA_TOKEN": token, "RALPH_JIRA_PROJECT": project} {
		if v == "" {
			t.Fatalf("%s must be set", name)
		}
	}
	typ := os.Getenv("RALPH_JIRA_TYPE")
	if typ == "" {
		typ = "Task"
	}
	c, err := New(Config{Site: site, User: user, Project: project, Token: func() (string, error) { return token, nil }})
	if err != nil {
		t.Fatal(err)
	}

	acc, err := c.Check(ctx)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	t.Logf("account %s", acc.Name)
	types, err := c.Types(ctx)
	if err != nil || len(types) == 0 {
		t.Fatalf("Types = %v, %v", types, err)
	}
	if !slices.Contains(types, typ) {
		t.Fatalf("the project has no type %q; it has %q (set RALPH_JIRA_TYPE)", typ, types)
	}

	ref := fmt.Sprintf("ralph-live-%d", time.Now().UnixNano())
	tk, err := c.Create(ctx, taskmanager.New{Type: typ, Title: "ralph live test", Body: "Created by the ralph live test.\n\n- one\n- two", Ref: ref})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if os.Getenv("RALPH_JIRA_DELETE") == "1" {
		t.Cleanup(func() {
			if err := c.deleteForTest(ctx, tk.Key); err != nil {
				t.Errorf("deleting %s: %v", tk.Key, err)
			}
		})
	} else {
		t.Logf("created ticket %s; delete it by hand", tk.Key)
	}
	if tk.Ref != ref || tk.Type != typ {
		t.Errorf("created = %+v", tk)
	}

	found, err := c.Find(ctx, ref)
	if err != nil || found == nil || found.Key != tk.Key {
		// Jira's search index can lag behind a creation by a few seconds.
		time.Sleep(5 * time.Second)
		if found, err = c.Find(ctx, ref); err != nil || found == nil || found.Key != tk.Key {
			t.Fatalf("Find = %+v, %v", found, err)
		}
	}

	f := taskmanager.Fields{Title: "ralph live test, updated", Body: "Updated body.\n\n1. first\n2. second"}
	if err := c.Update(ctx, tk.Key, f); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := c.Get(ctx, []string{tk.Key})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := c.Normalize(f)
	if g := got[tk.Key]; g.Title != want.Title || strings.TrimSpace(g.Body) != want.Body {
		t.Errorf("read back %q / %q, want %q / %q", g.Title, g.Body, want.Title, want.Body)
	}
}

// deleteForTest removes a ticket the live test created.
func (c *Client) deleteForTest(ctx context.Context, key string) error {
	return c.do(ctx, "DELETE", issuePath(key), nil, nil, nil)
}
