package cli

import (
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager/jira/jiratest"
)

func newQuickIssueEnv(t *testing.T) (*quickEnv, *jiratest.Server) {
	t.Helper()
	e := newQuickEnv(t, "")
	srv := jiratest.NewServer(t, jiratest.Options{})
	t.Setenv("RALPH_ISSUES_PROVIDER", "jira")
	t.Setenv("RALPH_JIRA_SITE", srv.URL())
	t.Setenv("RALPH_JIRA_USER", srv.User())
	t.Setenv("JIRA_TOKEN", srv.Token())
	return e, srv
}

func TestQuickCommandIssueFlagTitleOptional(t *testing.T) {
	e, srv := newQuickIssueEnv(t)
	key := srv.Seed(jiratest.Issue{Type: "Bug", Summary: "Fix the login", Status: "To Do", Body: "The page crashes."})
	e.start(t, "--issue", key)
	tasks := e.tasks(t)
	if len(tasks) != 1 || tasks[0].Title != "Fix the login" || !strings.Contains(tasks[0].Body, "The page crashes.") {
		t.Fatalf("tasks = %+v", tasks)
	}

	code, _, stderr := run(t, "quick", "start")
	if code == 0 || !strings.Contains(stderr, "accepts 1 arg(s)") {
		t.Fatalf("no title and no --issue: code %d, stderr %q", code, stderr)
	}
}

func TestQuickCommandIssueOutputLine(t *testing.T) {
	e, srv := newQuickIssueEnv(t)
	key := srv.Seed(jiratest.Issue{Type: "Bug", Summary: "Fix the login", Status: "In Progress", Body: "body"})
	out := e.start(t, "--issue", key)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 || lines[0] != "started T-1: Fix the login" || !strings.HasPrefix(lines[1], "branch: ") ||
		lines[2] != "issue: "+key+" Fix the login (In Progress)" || lines[3] != "verification: none configured" {
		t.Fatalf("output = %q", out)
	}
}

func TestQuickCommandIssueNoSourceExit1(t *testing.T) {
	e := newQuickEnv(t, "")
	code, _, stderr := run(t, "quick", "start", "--issue", "ABC-1")
	if code != 1 || !strings.Contains(stderr, "no issue source is configured: set issues.provider to jira") {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
	if tasks := e.tasks(t); len(tasks) != 0 {
		t.Fatalf("tasks = %+v", tasks)
	}
	if cur := e.repo.Git("branch", "--show-current"); cur != "main" {
		t.Fatalf("checkout on %q", cur)
	}
}

func TestQuickCommandIssueNotFoundExit1(t *testing.T) {
	e, srv := newQuickIssueEnv(t)
	srv.Seed(jiratest.Issue{Summary: "Other"})
	code, _, stderr := run(t, "quick", "start", "--issue", "ZZZ-999")
	if code != 1 || !strings.Contains(stderr, "ZZZ-999") {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
	if tasks := e.tasks(t); len(tasks) != 0 {
		t.Fatalf("tasks = %+v", tasks)
	}
	if cur := e.repo.Git("branch", "--show-current"); cur != "main" {
		t.Fatalf("checkout on %q", cur)
	}
}
