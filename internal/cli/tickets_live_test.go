package cli

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestLiveTickets creates a brief and a slice in a temporary project, syncs them to a real Jira Cloud site and
// changes only the tickets it created.
func TestLiveTickets(t *testing.T) {
	if os.Getenv("RALPH_JIRA_LIVE") != "1" {
		t.Skip("set RALPH_JIRA_LIVE=1, RALPH_JIRA_SITE, RALPH_JIRA_USER, RALPH_JIRA_TOKEN and RALPH_JIRA_PROJECT to run against a real Jira Cloud site (optional: RALPH_JIRA_TYPE_BRIEF, RALPH_JIRA_TYPE_SLICE, RALPH_JIRA_STATUS_TO, RALPH_JIRA_DELETE=1)")
	}
	site, user, token, project := os.Getenv("RALPH_JIRA_SITE"), os.Getenv("RALPH_JIRA_USER"), os.Getenv("RALPH_JIRA_TOKEN"), os.Getenv("RALPH_JIRA_PROJECT")
	for name, v := range map[string]string{"RALPH_JIRA_SITE": site, "RALPH_JIRA_USER": user, "RALPH_JIRA_TOKEN": token, "RALPH_JIRA_PROJECT": project} {
		if v == "" {
			t.Fatalf("RALPH_JIRA_LIVE is 1 but %s is not set", name)
		}
	}
	site = strings.TrimRight(site, "/")
	briefType, sliceType := liveEnv("RALPH_JIRA_TYPE_BRIEF", "Epic"), liveEnv("RALPH_JIRA_TYPE_SLICE", "Task")
	statusTo := os.Getenv("RALPH_JIRA_STATUS_TO")

	e := newE2EEnv(t)
	e.init(t)
	e.env = append(e.env, "JIRA_TOKEN="+token, "RALPH_JIRA_AUTO_SYNC=false")
	cfg := "taskmanager:\n  provider: jira\njira:\n  site: " + site + "\n  user: " + user + "\n  project: " + project + "\n" +
		"  type:\n    brief: " + briefType + "\n    slice: " + sliceType + "\n"
	if statusTo != "" {
		cfg += "  states:\n    - DONE=" + statusTo + "\n"
	}
	e.writeUserConfig(t, cfg)

	cs := e.connect(t, io.Discard)
	b := e2eCreate(t, cs, "brief", "", "ralph live test brief", "Created by the ralph live test.\n\n- one\n- two")
	s := e2eCreate(t, cs, "slice", b, "ralph live test slice", "A slice with **bold** words.\n\n1. first\n2. second")
	e2eCreate(t, cs, "task", s, "ralph live test task", "A task.")

	var made []string
	t.Cleanup(func() {
		if len(made) == 0 {
			return
		}
		if os.Getenv("RALPH_JIRA_DELETE") != "1" {
			t.Logf("created tickets %s; delete them by hand", strings.Join(made, ", "))
			return
		}
		for i := len(made) - 1; i >= 0; i-- {
			req, err := http.NewRequest(http.MethodDelete, site+"/rest/api/3/issue/"+made[i], nil)
			if err != nil {
				t.Errorf("deleting %s: %v", made[i], err)
				continue
			}
			req.SetBasicAuth(user, token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("deleting %s: %v", made[i], err)
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode >= 300 {
				t.Errorf("deleting %s: %s", made[i], resp.Status)
			}
		}
	})
	keyOf := func(id string) string {
		l := e2eLinks(t, cs, id)
		if len(l) != 1 || !strings.HasPrefix(l[0], "jira:") {
			t.Fatalf("links of %s = %v", id, l)
		}
		return strings.TrimPrefix(l[0], "jira:")
	}

	r := e.run(t, "tickets", "sync")
	for _, id := range []string{b, s} {
		if l := e2eLinks(t, cs, id); len(l) == 1 {
			made = append(made, strings.TrimPrefix(l[0], "jira:"))
		}
	}
	if r.code != 0 {
		t.Fatalf("tickets sync: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	bKey, sKey := keyOf(b), keyOf(s)

	brief := readIssue(t, site, user, token, bKey)
	slice := readIssue(t, site, user, token, sKey)
	if brief.Type != briefType || slice.Type != sliceType {
		t.Errorf("types %q and %q, want %q and %q", brief.Type, slice.Type, briefType, sliceType)
	}
	if slice.Parent != bKey {
		t.Errorf("the slice's parent is %q, want %s", slice.Parent, bKey)
	}
	if !strings.Contains(brief.Body, "- one") || !strings.Contains(slice.Body, "**bold**") || !strings.Contains(slice.Body, "1. first") {
		t.Errorf("descriptions came back as %q and %q", brief.Body, slice.Body)
	}

	e2eRetitle(t, cs, s, "ralph live test slice, renamed")
	if r := e.run(t, "tickets", "sync"); r.code != 0 {
		t.Fatalf("second sync: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if got := readIssue(t, site, user, token, sKey); got.Title != "ralph live test slice, renamed" {
		t.Errorf("the title is %q", got.Title)
	}

	if statusTo == "" {
		t.Log("RALPH_JIRA_STATUS_TO is not set; the status move is not checked")
		return
	}
	e2eSetState(t, cs, "T-1", "TODO")
	e2eSetState(t, cs, "T-1", "IN_PROGRESS")
	e2eSetState(t, cs, "T-1", "DONE")
	r = e.run(t, "tickets", "sync")
	t.Logf("sync after the task is done:\n%s%s", r.stdout, r.stderr)
	if got := readIssue(t, site, user, token, sKey); !strings.EqualFold(got.Status, statusTo) {
		t.Errorf("the ticket is in %q, want %q (sync exit %d)", got.Status, statusTo, r.code)
	}
}
