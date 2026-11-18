package cli

import (
	"os"
	"strings"
	"testing"
)

// TestLiveIssue reads one existing ticket from a real Jira Cloud site and starts quick work from it in a
// temporary repository. It only reads: the task manager is off, so nothing is written to Jira.
func TestLiveIssue(t *testing.T) {
	if os.Getenv("RALPH_JIRA_LIVE") != "1" {
		t.Skip("set RALPH_JIRA_LIVE=1, RALPH_JIRA_SITE, RALPH_JIRA_USER, RALPH_JIRA_TOKEN and RALPH_JIRA_ISSUE to read a ticket from a real Jira Cloud site")
	}
	ticket := os.Getenv("RALPH_JIRA_ISSUE")
	if ticket == "" {
		t.Skip("RALPH_JIRA_LIVE is 1 but RALPH_JIRA_ISSUE is not set: set it to the key of an existing ticket the account can read, such as ABC-1")
	}
	site, user, token := os.Getenv("RALPH_JIRA_SITE"), os.Getenv("RALPH_JIRA_USER"), os.Getenv("RALPH_JIRA_TOKEN")
	for name, v := range map[string]string{"RALPH_JIRA_SITE": site, "RALPH_JIRA_USER": user, "RALPH_JIRA_TOKEN": token} {
		if v == "" {
			t.Fatalf("RALPH_JIRA_LIVE is 1 but %s is not set", name)
		}
	}
	site = strings.TrimRight(site, "/")

	q := newQuickE2E(t)
	q.initAndCommit(t)
	q.env = append(q.env, "JIRA_TOKEN="+token)
	q.writeUserConfig(t, "issues:\n  provider: jira\ntaskmanager:\n  provider: none\njira:\n  site: "+site+"\n  user: "+user+"\n")

	shown := q.mustRun(t, "issue", "show", ticket)
	first, _, _ := strings.Cut(shown, "\n")
	fields := strings.SplitN(first, "  ", 4)
	if len(fields) != 4 || strings.TrimSpace(fields[3]) == "" {
		t.Fatalf("issue show did not print a title:\n%s", shown)
	}
	key := fields[0]
	if !strings.EqualFold(key, ticket) && !strings.Contains(ticket, "/"+key) {
		t.Logf("the ticket %s answered with key %s", ticket, key)
	}

	q.mustRun(t, "quick", "start", "--issue", ticket)
	branch := q.repo.Git("branch", "--show-current")
	if !strings.HasPrefix(branch, "ralph/"+strings.ToLower(key)+"-t-1-") {
		t.Errorf("branch = %q", branch)
	}
	if got := linkTargets(t, q.dir, "T-1"); got != "issue:jira:"+key+" jira:"+key {
		t.Errorf("links = %q", got)
	}
	if got := storeQuery(t, q.dir, "SELECT title FROM units WHERE id = 'T-1'"); got != strings.TrimSpace(fields[3]) && !strings.HasPrefix(strings.TrimSpace(fields[3]), got) {
		t.Errorf("task title = %q, ticket title = %q", got, fields[3])
	}
}
